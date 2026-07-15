package engine

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// conversation threads multi-turn history across runs. Each turn runs
// with the accumulated history as TaskSpec.Context; the run's result
// Messages (the runner strips the re-built-each-turn system prompt)
// become the next turn's context, so the agent sees its own prior tool
// calls and answers.
//
// run serializes turns under a mutex: a second submit blocks until the
// first turn finishes, then runs with that turn's history — sequential,
// continuous chat without concurrent runs corrupting the history.
type conversation struct {
	mu      sync.Mutex
	history []llm.Message
}

// run executes one turn via exec (the runner.Run call), threading the
// accumulated history in as Context and recording the result's messages
// for the next turn. Even on a terminal error the runner returns the
// history accumulated up to the failure, and every assistant tool_use is
// always paired with its tool result before any error path returns (the
// dispatch loop appends a result for each call unconditionally), so the
// partial history is coherent to thread back. Recording it preserves the
// turn's productive tool work instead of discarding it on a provider
// flake. Empty messages (e.g. an early setup error) leave history as-is.
//
// Run encodes terminal failures in the TaskResult (Reason=error) and
// publishes them as an OnConversationEnded(Reason=error) event, which the
// TUI surfaces as an error toast + log + idle-clear (see
// Session.applyConversationEnded). So there is nothing to return here — we
// keep only the partial history.
func (c *conversation) runSpecWithSetup(spec runner.TaskSpec, setup func() (func(runner.TaskSpec) runner.TaskResult, error)) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	spec.ID = taskscope.ID(uuid.NewString())
	repaired, changed := compact.RepairToolPairing(c.history)
	if changed > 0 {
		slog.Warn("turn: repaired unbalanced tool-call pairing before provider request",
			"stripped_or_dropped", changed, "before", len(c.history), "after", len(repaired))
	}
	c.history = repaired
	spec.Context = c.history
	exec, err := setup()
	if err != nil {
		return err
	}
	res := exec(spec)
	if len(res.Messages) > 0 {
		c.history = res.Messages
	}
	return nil
}

func (c *conversation) run(prompt string, exec func(runner.TaskSpec) runner.TaskResult) {
	c.runSpec(runner.TaskSpec{Prompt: prompt}, exec)
}

func (c *conversation) runSpec(spec runner.TaskSpec, exec func(runner.TaskSpec) runner.TaskResult) {
	c.mu.Lock()
	defer c.mu.Unlock()

	spec.ID = taskscope.ID(uuid.NewString())
	repaired, changed := compact.RepairToolPairing(c.history)
	if changed > 0 {
		slog.Warn("turn: repaired unbalanced tool-call pairing before provider request",
			"stripped_or_dropped", changed, "before", len(c.history), "after", len(repaired))
	}
	c.history = repaired
	spec.Context = c.history
	res := exec(spec)
	if len(res.Messages) > 0 {
		c.history = res.Messages
	}
}

func (c *conversation) snapshot() []llm.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]llm.Message, len(c.history))
	copy(out, c.history)
	return out
}

// restore replaces the conversation history with a persisted transcript. A
// saved blob can be partially written (crash mid-save) or externally edited,
// leaving a tool_use without its tool_result (or vice-versa) — which strict
// providers reject with a 400 on every subsequent turn, permanently bricking
// -continue for that session. RepairToolPairing rebalances the pairing on the
// way in so a corrupt blob degrades to a warning + truncation instead.
func (c *conversation) restore(history []llm.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	repaired, changed := compact.RepairToolPairing(history)
	if changed > 0 {
		slog.Warn("restore: repaired unbalanced tool-call pairing in saved history",
			"stripped_or_dropped", changed, "before", len(history), "after", len(repaired))
	}
	// RepairToolPairing always returns a freshly allocated slice, so it is
	// already independent of the caller's backing array.
	c.history = repaired
}

func (c *conversation) compactNow(ctx context.Context, compactor compact.Compactor, sink runner.EventSink) (ManualCompactionResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	before := len(c.history)
	if before == 0 {
		return ManualCompactionResult{MessagesBefore: 0, MessagesAfter: 0, Engine: compact.EngineTiered}, nil
	}
	repaired, changed := compact.RepairToolPairing(c.history)
	if changed > 0 {
		slog.WarnContext(ctx, "manual compact: repaired unbalanced tool-call pairing before compaction",
			"stripped_or_dropped", changed, "before", len(c.history), "after", len(repaired))
	}
	c.history = repaired
	before = len(c.history)
	keep := compact.AdaptiveKeepRecent(c.history, coderunner.AdaptiveKeepTargetTokens, coderunner.AdaptiveKeepMin, coderunner.AdaptiveKeepMax)
	res, err := compactor.Compact(ctx, c.history, keep)
	if err != nil {
		return ManualCompactionResult{}, fmt.Errorf("compact now: %w", err)
	}
	if len(res.History) > 0 {
		c.history = res.History
	}
	after := len(c.history)
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
