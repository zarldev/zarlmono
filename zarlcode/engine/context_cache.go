package engine

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	agentcompact "github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// ContextCache threads multi-turn context across runs. Each turn runs
// with the accumulated context as TaskSpec.Context; the run's result
// Messages (the runner strips the re-built-each-turn system prompt)
// become the next turn's context, so the agent sees its own prior tool
// calls and answers.
//
// Run serializes turns under a mutex: a second submit blocks until the
// first turn finishes, then runs with that turn's context — sequential,
// continuous chat without concurrent runs corrupting the context.
type ContextCache struct {
	mu      sync.Mutex
	context []llm.Message
}

// run executes one turn via exec (the runner.Run call), threading the
// accumulated context in as Context and recording the result's messages
// for the next turn. Even on a terminal error the runner returns the
// context accumulated up to the failure, and every assistant tool_use is
// always paired with its tool result before any error path returns (the
// dispatch loop appends a result for each call unconditionally), so the
// partial context is coherent to thread back. Recording it preserves the
// turn's productive tool work instead of discarding it on a provider
// flake. Empty messages (e.g. an early setup error) leave context as-is.
//
// Run encodes terminal failures in the TaskResult (Reason=error) and
// publishes them as an OnConversationEnded(Reason=error) event, which the
// TUI surfaces as an error toast + log + idle-clear (see
// Session.applyConversationEnded). So there is nothing to return here — we
// keep only the partial context.
func (c *ContextCache) transition(spec runner.TaskSpec, setup func() (func(runner.TaskSpec) runner.TaskResult, error)) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	spec.ID = taskscope.ID(uuid.NewString())
	repaired, _ := agentcompact.RepairToolPairing(c.context)
	spec.Context = cloneMessages(repaired)
	exec, err := setup()
	if err != nil {
		return err
	}
	c.context = cloneMessages(repaired)
	res := exec(spec)
	if len(res.Messages) > 0 {
		c.context = cloneMessages(res.Messages)
	}
	return nil
}

func (c *ContextCache) run(prompt string, exec func(runner.TaskSpec) runner.TaskResult) {
	c.runSpec(runner.TaskSpec{Prompt: prompt}, exec)
}

func (c *ContextCache) runSpec(spec runner.TaskSpec, exec func(runner.TaskSpec) runner.TaskResult) {
	_ = c.transition(spec, func() (func(runner.TaskSpec) runner.TaskResult, error) { return exec, nil })
}

func (c *ContextCache) snapshot() []llm.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return cloneMessages(c.context)
}

func cloneMessages(messages []llm.Message) []llm.Message {
	cloned := make([]llm.Message, len(messages))
	for i, message := range messages {
		message.Parts = cloneContentParts(message.Parts)
		message.ToolCalls = append([]llm.ToolCall(nil), message.ToolCalls...)
		cloned[i] = message
	}
	return cloned
}

func cloneContentParts(parts []llm.ContentPart) []llm.ContentPart {
	if parts == nil {
		return nil
	}
	cloned := make([]llm.ContentPart, len(parts))
	for i, part := range parts {
		if part.Image != nil {
			image := *part.Image
			part.Image = &image
		}
		if part.Audio != nil {
			audio := *part.Audio
			part.Audio = &audio
		}
		if part.Video != nil {
			video := *part.Video
			part.Video = &video
		}
		cloned[i] = part
	}
	return cloned
}

// restore replaces the context cache with a persisted context cache. A
// saved blob can be partially written (crash mid-save) or externally edited,
// leaving a tool_use without its tool_result (or vice-versa) — which strict
// providers reject with a 400 on every subsequent turn, permanently bricking
// -continue for that session. RepairToolPairing rebalances the pairing on the
// way in so a corrupt blob degrades to a warning + truncation instead.
func (c *ContextCache) restore(context []llm.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	repaired, changed := agentcompact.RepairToolPairing(cloneMessages(context))
	if changed > 0 {
		slog.Warn("restore: repaired unbalanced tool-call pairing in saved context",
			"stripped_or_dropped", changed, "before", len(context), "after", len(repaired))
	}
	c.context = repaired
}

func (c *ContextCache) compactNow(ctx context.Context, compactor agentcompact.Compactor, sink runner.EventSink) (ManualCompactionResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	before := len(c.context)
	if before == 0 {
		return ManualCompactionResult{MessagesBefore: 0, MessagesAfter: 0, Engine: agentcompact.EngineTiered}, nil
	}
	repaired, changed := agentcompact.RepairToolPairing(c.context)
	if changed > 0 {
		slog.WarnContext(ctx, "manual compact: repaired unbalanced tool-call pairing before compaction",
			"stripped_or_dropped", changed, "before", len(c.context), "after", len(repaired))
	}
	c.context = repaired
	before = len(c.context)
	keep := agentcompact.AdaptiveKeepRecent(c.context, coderunner.AdaptiveKeepTargetTokens, coderunner.AdaptiveKeepMin, coderunner.AdaptiveKeepMax)
	res, err := compactor.Compact(ctx, cloneMessages(c.context), keep)
	if err != nil {
		return ManualCompactionResult{}, fmt.Errorf("compact now: %w", err)
	}
	if len(res.History) > 0 {
		c.context = cloneMessages(res.History)
	}
	after := len(c.context)
	out := ManualCompactionResult{MessagesBefore: before, MessagesAfter: after, BytesTrimmed: res.BytesTrimmed, Engine: res.Engine}
	if sink != nil && (before != after || res.BytesTrimmed > 0) {
		sink.OnCompactionApplied(runner.CompactionApplied{
			TaskID:         "manual-compact",
			MessagesBefore: before,
			MessagesAfter:  after,
			BytesTrimmed:   res.BytesTrimmed,
			Engine:         res.Engine,
		})
	}
	return out, nil
}

// Run executes a turn and threads its returned messages into subsequent turns.
func (c *ContextCache) Run(prompt string, exec func(runner.TaskSpec) runner.TaskResult) {
	c.run(prompt, exec)
}

// Restore replaces context after repairing incomplete tool-call pairs.
func (c *ContextCache) Restore(context []llm.Message) { c.restore(context) }

// Compact applies compactor to the current context without changing context on failure.
func (c *ContextCache) Compact(ctx context.Context, compactor agentcompact.Compactor, sink runner.EventSink) (ManualCompactionResult, error) {
	return c.compactNow(ctx, compactor, sink)
}

// Snapshot returns a concurrency-safe copy of the context cache.
func (c *ContextCache) Snapshot() []llm.Message { return c.snapshot() }
