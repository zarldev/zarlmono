package runner

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/repair"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// loopState groups the per-Run counters threaded through the iteration
// loop, so the loop stops growing a fresh local per recovery mechanism.
// The three recovery budgets each reset to zero on a healthy stream; the
// finalize-warn latch fires once per Run.
type loopState struct {
	// toolCallJSONRecovers counts consecutive soft-recoveries from
	// upstream "malformed tool-call JSON" 500s. Resets on any successful
	// iteration; once it exceeds toolCallJSONRecoverLimit the error goes
	// terminal. See [isUpstreamToolCallJSONError].
	toolCallJSONRecovers int
	// emptyStreamRetries counts consecutive retries of an empty-stream
	// iteration (ErrEmptyStream). Resets on any stream that produced
	// output; capped at emptyStreamRetryLimit. See
	// [isEmptyStreamDecodeError].
	emptyStreamRetries int
	// thinkingBudgetCuts counts consecutive recoveries of an
	// ErrThinkingBudget cut (a turn that ran past the thinking-only byte
	// budget). Resets on any stream that produced output; capped at
	// thinkingBudgetRecoverLimit.
	thinkingBudgetCuts int
	// turnQualityCorrections counts empty-turn corrections injected by the
	// TurnQuality hook, bounded by the decision's MaxCorrections.
	turnQualityCorrections int
	// rateLimitRetries counts consecutive provider rate-limit recoveries.
	// Resets on any successful iteration; capped at rateLimitRetryLimit.
	rateLimitRetries int
	// finalizeWarned latches the cap-warning nudge to exactly once per Run.
	// The nudge rides a single request at shape time — NEVER appended to
	// canonical history, so the synthetic "wrap up" message isn't
	// persisted, threaded into the next turn, or sent to a sub-agent.
	finalizeWarned bool
	// mutatingCalls counts successful mutating tool calls (ToolSpec.Mutates
	// && ToolResult.Success) across the Run — the "did the agent actually
	// change anything" signal the CompletionGate reads. Only maintained
	// when a gate is installed (zero overhead otherwise).
	mutatingCalls int
	// completionCorrections counts holds injected by the CompletionGate,
	// bounded by the decision's MaxCorrections. Unlike the finalize-warn
	// nudge, the gate's correction IS appended to canonical history — the
	// model must see it on the next turn to act on it.
	completionCorrections int
	// forceCompactNoopAt is the message count at which a token-pressure
	// forced compaction last freed nothing (history dominated by untrimmable
	// content — e.g. one huge user message). Until at least keepRecent new
	// messages accrue (pushing older ones out of the keep window so there's
	// something fresh to trim), the runner skips re-running a forced compact
	// it knows will no-op. Zero = no active latch; reset whenever a compaction
	// actually trims.
	forceCompactNoopAt int
}

// Run executes a task to completion or terminal condition. The loop is:
//
//  1. Plant the current depth on ctx so spawn-agent can read it.
//  2. Yield to ConversationLock if active.
//  3. Drain the Steerer for any queued user-side messages.
//  4. Build messages: [system?, ...spec.Context, user prompt + accumulated tool results].
//  5. Stream Provider.Complete; publish chunks as LLM events.
//  6. Extract structured tool calls; fall back to ParseFromText if none.
//  7. Dispatch each call through the tool registry. Append results
//     to the message history.
//  8. Terminate when the model emits no more tool calls, when ctx
//     is cancelled, on max iterations, or on a provider error.
//
// The runner is safe to reuse across concurrent Runs — internal state
// (provider, registries) is read-only during a run; per-task state
// (message history, accumulated state) is local to this method.
func (r *Runner) Run(ctx context.Context, spec TaskSpec) TaskResult {
	start := time.Now()

	// Validate + normalise spec. Every terminal condition — including
	// invalid input and setup failure — is encoded in the returned
	// TaskResult (Reason + Err); Run has no separate error channel, so a
	// consumer reads one place and the harness needs no precedence rules.
	if spec.ID == "" {
		spec.ID = taskscope.ID(uuid.NewString())
	}
	if spec.MaxIterations < 0 {
		// Publish the paired Started/Ended bookend like every other terminal
		// path — a consumer that reacts only to the event stream (zarlcode's
		// conversation wrapper discards TaskResult.Err) must not see the turn
		// vanish silently. publishSetupFailed reads spec, not planted ctx.
		r.publishSetupFailed(ctx, spec, start, ErrInvalidIterations)
		return TaskResult{ID: spec.ID, Reason: TerminalError, Err: ErrInvalidIterations}
	}

	// Plant the current depth on ctx so spawn-agent (or other tools
	// that grow into the same need) can read it without a back-channel.
	// TaskID is planted alongside so wrappers (MemoSource, budget
	// enforcers) can bucket per-task state.
	ctx = taskscope.WithDepth(ctx, spec.Depth)
	ctx = taskscope.WithID(ctx, spec.ID)
	if tf, ok := r.tools.(interface{ ForgetTask(taskscope.ID) }); ok {
		defer tf.ForgetTask(spec.ID)
	}

	maxIter := cmp.Or(spec.MaxIterations, r.maxIterations)

	// Initial messages: optional system prompt + spec.Context + user prompt.
	messages, err := r.initialMessages(ctx, spec)
	if err != nil {
		r.publishSetupFailed(ctx, spec, start, err)
		return TaskResult{ID: spec.ID, Reason: TerminalError, Err: err}
	}

	r.publishConversationStarted(ctx, spec)

	// All mutable per-run state — history, usage accounting, recovery
	// budgets, request policy — lives on one taskRun value (see
	// taskrun.go); the terminal exits and the stream-error ladder are its
	// methods, so the loop body stays the only writer.
	t := &taskRun{
		r:        r,
		spec:     spec,
		start:    start,
		maxIter:  maxIter,
		messages: messages,
		thinking: spec.Thinking,
	}
	for iter := range maxIter {
		t.iter = iter
		if err := ctx.Err(); err != nil {
			return t.cancelled(ctx, err)
		}
		slog.InfoContext(ctx, "runner: iter start", "task", string(spec.ID), "iter", iter, "messages", len(t.messages))
		// Yield to real-time conversation if applicable. The lock
		// uses sync.Cond + context.AfterFunc; no polling, immediate
		// resume on Release.
		if r.convLock != nil {
			if err := r.convLock.Wait(ctx); err != nil {
				return t.cancelled(ctx, fmt.Errorf("%w: %w", ErrCancelled, err))
			}
		}

		// Steerer hook: drain any user messages queued by an
		// interactive harness between iterations and inject them
		// into history before shaping the next request. AppendSeq
		// goes straight from the iter.Seq into the message slice
		// without an intermediate buffer; the post-append slice
		// suffix is what we publish.
		if r.steerer != nil {
			before := len(t.messages)
			t.messages = slices.AppendSeq(t.messages, r.steerer.Drain(ctx))
			if len(t.messages) > before {
				r.publishSteerInjected(ctx, spec, t.messages[before:])
			}
		}

		// Finalize-warn hook: when remaining iterations drop into the
		// configured threshold window, inject a one-shot "wrap up"
		// user message so the model has explicit warning that the cap
		// is close. Ordered between steerer (user-side priority wins)
		// and compactor (compaction sees the augmented history).
		// Sits inside the loop body so a Run that aborts early (via
		// ctx cancel or stream error) before reaching the threshold
		// never injects.
		var finalizeNudge string // request-only; non-empty on the one trip iteration
		if !t.st.finalizeWarned && r.finalizeWarn.RemainingThreshold > 0 {
			remaining := maxIter - iter
			if remaining <= r.finalizeWarn.RemainingThreshold {
				t.st.finalizeWarned = true
				finalizeNudge = finalizeWarnMessage(remaining, r.finalizeWarn.Message)
			}
		}

		// Auto-compaction policy: skips iter 0, applies the token-pressure
		// force-path + Prober gate, and trims history when warranted. A
		// non-nil error is the terminal ErrCompact failure. See autocompact.go.
		if cerr := t.maybeCompact(ctx); cerr != nil {
			return t.errored(ctx, cerr)
		}

		shaped := r.template.ShapeMessages(t.messages, t.thinking)
		if finalizeNudge != "" {
			// Request-only: the nudge rides this iteration's request but
			// never enters the canonical history (see finalizeNudge above).
			// Cap the slice so append allocates rather than writing into a
			// backing array shaped might share with messages.
			shaped = append(shaped[:len(shaped):len(shaped)], llm.Message{Role: llm.RoleUser, Content: finalizeNudge})
		}
		llmTools := r.buildLLMTools(ctx)
		req := llm.CompletionRequest{
			Messages:           shaped,
			Tools:              llmTools,
			Stream:             true,
			MaxTokens:          r.maxTokens,
			Temperature:        r.temperature,
			Thinking:           llm.ThinkingConfig{Enabled: t.thinking},
			ChatTemplateKwargs: r.template.ThinkingKwargs(t.thinking),
		}

		// Per-iteration ctx so we can bail cleanly on
		// iterationTimeout / streamIdleTimeout without killing the
		// outer Run ctx (the caller may want to keep going on the
		// next iteration). When both timeouts are 0, this is just
		// ctx — no-op wrapper.
		iterCtx, cancelIter := iterationContext(ctx, r.timeouts.iteration)
		slog.InfoContext(
			ctx,
			"runner: calling complete",
			"task",
			string(spec.ID),
			"iter",
			iter,
			"messages",
			len(shaped),
		)
		callStart := time.Now()
		stream, err := r.client.Complete(iterCtx, req)
		slog.InfoContext(
			ctx,
			"runner: complete returned",
			"task",
			string(spec.ID),
			"iter",
			iter,
			"elapsed_ms",
			time.Since(callStart).Milliseconds(),
			"err",
			err,
		)
		if err != nil {
			cancelIter()
			return t.errored(ctx, fmt.Errorf("complete: %w", err))
		}

		// Drain the completion stream: accumulate content / thinking /
		// tool calls, run the idle watchdog + producer goroutine,
		// classify the terminal condition, and cancelIter. See drain.go.
		sr := r.drainStream(ctx, iterCtx, cancelIter, spec, iter, stream)
		toolCalls := sr.toolCalls
		toolCallOrder := sr.toolCallOrder
		streamErr := sr.err
		// t.lastUsage tracks the most recent usage across the whole Run
		// (the next iteration's token-pressure compaction reads it), so
		// only update it when this stream actually carried usage —
		// otherwise retain the prior iteration's value. iterUsage is this
		// iteration's contribution (nil when the provider omitted usage),
		// folded into totalUsage exactly once below.
		iterUsage := sr.usage
		if iterUsage != nil {
			t.lastUsage = iterUsage
		}

		if streamErr != nil {
			if terminal := t.recoverStreamErr(ctx, streamErr); terminal != nil {
				return *terminal
			}
			continue // retry — recovery may have appended a corrective turn
		}
		// Stream succeeded — clear the soft-recovery budgets so a later
		// failure in the same task gets the full retry/recovery allowance
		// (a stream that produced output means the gateway is healthy and
		// the model isn't wedged in reasoning).
		t.st.toolCallJSONRecovers = 0
		t.st.emptyStreamRetries = 0
		t.st.thinkingBudgetCuts = 0
		t.st.rateLimitRetries = 0
		// Rewrite each tool call's arguments to canonical JSON before they
		// land in history — see canonicalizeToolArgs.
		canonicalizeToolArgs(toolCalls)
		// Fold this iteration's reported usage into the run-wide
		// total. Done here (post-drain, on success) so we attribute
		// once per iteration and skip iterations that errored out
		// mid-stream (those callers already see the partial spend
		// via the terminal LastUsage). Providers that omit Usage on
		// some chunks leave iterUsage nil — that iteration silently
		// contributes nothing rather than double-counting the
		// previous iter's snapshot.
		t.totalUsage = addUsage(t.totalUsage, iterUsage)

		assistantContent := strings.TrimSpace(sr.content)

		// Tool fallback: if no structured calls, try parsing them from the
		// visible text — see appendTextToolCalls.
		clean := assistantContent
		if len(toolCalls) == 0 && clean != "" {
			toolCallOrder, clean = appendTextToolCalls(clean, toolCalls, toolCallOrder)
		}
		t.finalContent = clean

		// Append the assistant message (text + tool calls) to history.
		// Content and ReasoningContent stay disjoint — providers route
		// reasoning to the out-of-band Thinking channel and the runner
		// stores it under ReasoningContent so per-provider history
		// serializers (Inline / Field / Strip) can reshape it for the
		// next request without re-parsing the visible body.
		assistantMsg := llm.Message{
			Role:             llm.RoleAssistant,
			Content:          assistantContent,
			ReasoningContent: sr.thinking,
		}
		if len(toolCallOrder) > 0 {
			assistantMsg.ToolCalls = make([]llm.ToolCall, 0, len(toolCallOrder))
			for _, id := range toolCallOrder {
				assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, *toolCalls[id])
			}
		}
		t.messages = append(t.messages, assistantMsg)

		// TurnQuality hook: catch degenerate "empty content + no tool
		// calls" turns before they exit the loop as a successful but
		// content-less TaskResult. The thinking-budget failure mode —
		// model burns max_tokens on thinking and emits nothing the
		// user sees — falls into this bucket and would otherwise
		// stall the conversation. The hook returns a decision with a
		// correction when it wants the runner to inject a follow-up and
		// continue; a zero decision means "this turn is fine, proceed
		// with normal branching." Only consulted when the structured
		// tool-call slice is empty — the dispatch path already covers
		// turns with tools.
		if r.turnQuality != nil && len(toolCallOrder) == 0 {
			decision := r.turnQuality.Inspect(clean, nil)
			if decision.Correction != "" && (decision.MaxCorrections == 0 || t.st.turnQualityCorrections < decision.MaxCorrections) {
				t.st.turnQualityCorrections++
				t.messages = append(t.messages, llm.Message{Role: llm.RoleUser, Content: decision.Correction})
				if decision.DisableThinking {
					t.thinking = false // permanent for this Run — see taskRun.thinking
				}
				cancelIter()
				continue
			}
		}

		// No tool calls: we're done — record the final content and exit.
		if len(toolCalls) == 0 {
			// Completion gate: refuse a premature "done" when the run made
			// no durable change (no successful mutating tool call). Inject a
			// corrective user turn and continue so the model can make the
			// change within THIS Run — preventing an empty-patch attempt
			// rather than retrying after the fact. Skip on the last
			// iteration (no turn left to act on the correction) and once the
			// MaxCorrections budget is spent, so a genuinely stuck model
			// still terminates cleanly.
			if r.completionGate != nil && iter < maxIter-1 {
				decision := r.completionGate.Inspect(t.st.mutatingCalls > 0, clean)
				if decision.Correction != "" &&
					(decision.MaxCorrections == 0 || t.st.completionCorrections < decision.MaxCorrections) {
					t.st.completionCorrections++
					slog.InfoContext(ctx, "runner: completion gate held",
						"task", string(spec.ID), "iter", iter,
						"mutating_calls", t.st.mutatingCalls,
						"corrections", t.st.completionCorrections)
					t.messages = append(t.messages, llm.Message{Role: llm.RoleUser, Content: decision.Correction})
					continue
				}
			}
			// Usage=occupancy, Delta=this iteration's own usage — see
			// IterationCompleted. Identical to the post-dispatch site below.
			r.publishIterationCompleted(ctx, spec, iter, iterUsage, t.lastUsage, t.messages)
			if uo, ok := r.compactor.(compact.UsageObserver); ok {
				uo.ObserveUsage(t.lastUsage)
			}
			return t.completed(ctx)
		}

		// Dispatch tool calls. Registry tools may run in parallel up to
		// r.toolConcurrency, but their results are reassembled in the
		// original toolCallOrder so the LLM sees tool messages in the
		// order it emitted them.
		dispatchStart := time.Now()
		slog.InfoContext(
			ctx,
			"runner: dispatch start",
			"task",
			string(spec.ID),
			"iter",
			iter,
			"n_calls",
			len(toolCallOrder),
		)
		dispatched := r.dispatchBatch(ctx, spec, toolCalls, toolCallOrder)
		slog.InfoContext(ctx, "runner: dispatch done", "task", string(spec.ID), "iter", iter,
			"elapsed_ms", time.Since(dispatchStart).Milliseconds(), "n_calls", len(toolCallOrder))
		for _, id := range toolCallOrder {
			tc := toolCalls[id]
			d := dispatched[id]
			// Completion-gate bookkeeping: a successful mutating tool call
			// is the "real work happened" signal. Gated on completionGate
			// being installed so the spec lookup is skipped entirely on the
			// default path.
			if r.completionGate != nil && d.err == nil && d.result != nil && d.result.Success &&
				r.toolMutates(ctx, tc.Function.Name) {
				t.st.mutatingCalls++
			}
			if d.err != nil {
				// Cancel / timeout aren't faults — they're the user's
				// own ctx unwinding through every in-flight tool.
				// Drop to Debug so consumers with stdout-tee'd slog
				// (e.g. an zarlcode TUI) don't get the runtime
				// noise painted over the alt-screen.
				if errors.Is(d.err, context.Canceled) || errors.Is(d.err, context.DeadlineExceeded) {
					slog.DebugContext(ctx, "runner: tool cancelled",
						"task_id", spec.ID,
						llm.RoleTool, tc.Function.Name,
						"err", d.err)
				} else {
					slog.WarnContext(ctx, "runner: tool dispatch failed",
						"task_id", spec.ID,
						llm.RoleTool, tc.Function.Name,
						"err", d.err)
				}
			}
			t.messages = append(t.messages, llm.Message{
				Role:       llm.RoleTool,
				Content:    r.toolResultText(d.result, tc.Function.Name),
				ToolCallID: tc.ID,
			})
		}
		t.totalToolCalls += len(toolCallOrder)
		// Persist progress after dispatch so a SIGKILL on the outer
		// ctx during the *next* iteration's LLM call still leaves a
		// row reflecting "we got to iter N with M tool calls" — the
		// information needed to distinguish "agent never started" from
		// "agent worked hard, just ran out of time".
		if r.progressUpdater != nil {
			r.progressUpdater(ctx, iter+1, t.totalToolCalls)
		}
		// Usage=occupancy, Delta=this iteration's own usage — see
		// IterationCompleted. The compaction gate (compact.PressureGated)
		// reads occupancy, which never goes nil mid-Run even when this
		// turn's provider dropped usage.
		r.publishIterationCompleted(ctx, spec, iter, iterUsage, t.lastUsage, t.messages)
		if uo, ok := r.compactor.(compact.UsageObserver); ok {
			uo.ObserveUsage(t.lastUsage)
		}
	}

	// Loop exited without terminal condition.
	return t.maxedOut(ctx)
}

// upstreamToolCallJSONRecoveryMessage is the corrective user turn injected
// when the upstream LLM server rejects the model's tool-call arguments as
// malformed JSON before any chunk reaches our dispatch-side repair. It
// gives the model one targeted chance to re-emit with valid escaping.
// thinkingBudgetRecoveryMessage is the corrective user turn injected when
// an iteration is cut for running past the thinking-only budget — minutes
// of reasoning with nothing to show. It forces the model out of the
// reasoning loop and onto a concrete next action.
const thinkingBudgetRecoveryMessage = "Your previous turn spent its entire thinking budget without producing any answer or tool call. " +
	"Stop reasoning now and take a concrete action: either make your next tool call (read/edit/bash/…) or, if you have enough to conclude, write your final answer in plain text. " +
	"Do not open another long chain of thought — commit to a step with what you already know."

const upstreamToolCallJSONRecoveryMessage = "Your previous response was rejected by the LLM server because the tool-call arguments JSON was malformed " +
	"(common cause: literal newlines or unescaped quotes inside a string value). " +
	"Re-emit the same tool call with valid JSON: escape newlines as \\n, escape quotes as \\\", " +
	"and keep multi-line code strings on a single JSON line. Try again now."

// canonicalizeToolArgs rewrites each tool call's argument JSON in place to a
// canonical form. The model sometimes emits malformed JSON (unescaped
// newlines in long paths, missing closers when the response gets cut off);
// repair.Unmarshal would recover it later in dispatch, but if the raw text
// stays in tc.Function.Arguments the malformed string lands in conversation
// history and chokes the next request server-side (llama-server's
// chat.cpp:func_args_not_string used to throw 500 on it). Args that can't be
// salvaged are left as-is so dispatch surfaces a Validation result and the
// model gets a targeted re-emit message.
func canonicalizeToolArgs(toolCalls map[string]*llm.ToolCall) {
	for _, tc := range toolCalls {
		if tc.Function.Arguments == "" {
			continue
		}
		var v any
		if err := repair.Unmarshal([]byte(tc.Function.Arguments), &v); err != nil {
			continue
		}
		canonical, err := json.Marshal(v)
		if err != nil {
			continue
		}
		tc.Function.Arguments = string(canonical)
	}
}

// appendTextToolCalls is the no-structured-calls fallback: when the model
// wrote its tool calls as text rather than structured tool_call objects,
// ParseFromText extracts them. Parsed calls are added to the toolCalls map
// and appended to order; the returned order and the remaining non-tool text
// (the visible content with the tool syntax stripped) replace the caller's
// locals. When nothing parses, order and clean come back unchanged.
func appendTextToolCalls(clean string, toolCalls map[string]*llm.ToolCall, order []string) ([]string, string) {
	fallback, remaining := tools.ParseFromText(clean)
	if len(fallback) == 0 {
		return order, clean
	}
	for _, fc := range fallback {
		id := uuid.NewString()
		argBytes, _ := json.Marshal(fc.Arguments)
		toolCalls[id] = &llm.ToolCall{
			ID:   id,
			Type: "function",
			Function: llm.ToolCallFunction{
				Name:      string(fc.Name),
				Arguments: string(argBytes),
			},
		}
		order = append(order, id)
	}
	return order, remaining
}

// addUsage folds add into base and returns the new accumulator.
// The returned *llm.Usage is always a fresh allocation — the input
// pointers are never mutated — so a chunk pointer reuse on the
// provider side can't mutate prior totals retroactively. Either
// argument may be nil — nil means "no contribution" rather than
// "zero contribution" (the latter would still imply a successful
// usage-bearing iteration). When add is nil, base itself is returned
// untouched (no allocation); when base is nil, add is copied.
func addUsage(base, add *llm.Usage) *llm.Usage {
	if add == nil {
		return base
	}
	if base == nil {
		copied := *add
		return &copied
	}
	acc := *base
	acc.PromptTokens += add.PromptTokens
	acc.CompletionTokens += add.CompletionTokens
	acc.TotalTokens += add.TotalTokens
	acc.CachedTokens += add.CachedTokens
	return &acc
}

// systemPromptFrom returns the content of the leading system message,
// or "" when there is none. The system message is preserved at index 0
// of the live history across every compaction engine (Summary/Executive
// carry the leading slice verbatim, Tiered pins head=1, Structural never
// trims a system role), so reading messages[0] at a terminal site
// recovers the exact prompt the run used without threading it through
// every builder signature.
func systemPromptFrom(messages []llm.Message) string {
	if len(messages) > 0 && messages[0].Role == llm.RoleSystem {
		return messages[0].Content
	}
	return ""
}

// stripSystem returns messages without any leading system message.
// REPL callers re-apply the system prompt each turn from the
// PromptSource, so persisting it would double it up; everything
// else (user / assistant / tool) is what carries the conversation
// forward. Clones so the caller's slice is independent of the
// runner's internal history.
func stripSystem(msgs []llm.Message) []llm.Message {
	if len(msgs) > 0 && msgs[0].Role == llm.RoleSystem {
		return slices.Clone(msgs[1:])
	}
	return slices.Clone(msgs)
}

// initialMessages assembles the message history a fresh task starts
// with: optional system prompt + spec.Context + user prompt. The
// system prompt comes from the installed PromptSource (nil = no
// system message).
func (r *Runner) initialMessages(ctx context.Context, spec TaskSpec) ([]llm.Message, error) {
	var msgs []llm.Message

	if r.prompt != nil {
		system, err := r.prompt.System(ctx, spec.PromptVars)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrPromptRender, err)
		}
		if system != "" {
			msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: system})
		}
	}

	msgs = append(msgs, spec.Context...)

	if spec.Prompt != "" || len(spec.Attachments) > 0 {
		msg := llm.Message{Role: llm.RoleUser, Content: spec.Prompt}
		if len(spec.Attachments) > 0 {
			msg.Parts = make([]llm.ContentPart, 0, 1+len(spec.Attachments))
			if spec.Prompt != "" {
				msg.Parts = append(msg.Parts, llm.TextPart(spec.Prompt))
			}
			msg.Parts = append(msg.Parts, spec.Attachments...)
		}
		msgs = append(msgs, msg)
	}
	return msgs, nil
}

func (r *Runner) toolResultText(result *tools.ToolResult, toolName string) string {
	if result == nil {
		return ""
	}
	if !result.Success {
		var msg string
		if result.Error != "" {
			msg = "ERROR: " + result.Error + toolErrorHint(result.Err)
		} else {
			msg = "ERROR: tool reported failure"
		}
		return r.truncator.Truncate(msg, toolName)
	}
	if result.Data == nil {
		return "ok"
	}
	return r.truncator.Truncate(formatToolData(result.Data), toolName)
}

func toolErrorHint(err *tools.Error) string {
	if err == nil {
		return ""
	}
	switch err.Kind {
	case tools.Kinds.VALIDATION:
		return " | check the tool schema and fix the argument"
	case tools.Kinds.PERMISSION:
		return " | try a non-mutating tool or ask for access"
	case tools.Kinds.TRANSIENT:
		return " | retry once or verify state"
	case tools.Kinds.BUDGET:
		return " | reduce scope, avoid deep reads, or delegate via spawn_agent"
	case tools.Kinds.FATAL:
		return " | cannot recover — stop retrying and explain the issue"
	case tools.Kinds.STALE:
		return " | perform a fresh read of the target area, then retry with fresh hashes"
	default:
		return ""
	}
}

// formatToolData renders a tool result's structured Data as text: a
// string verbatim, a fmt.Stringer via String() (typed results like grep's
// GrepResult supply their model-facing text this way; the structured
// value still rides the event), anything else as capped JSON. Shared by
// the message text (toolResultText, which truncates on top) and the
// ToolCompleted event's FormattedResult so the two can't diverge — and a
// nested map renders as JSON rather than fmt.Sprint's Go syntax.
func formatToolData(data any) string {
	switch v := data.(type) {
	case nil:
		return ""
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return marshalCapped(v)
	}
}

// marshalCappedLimit is the byte ceiling on JSON-encoded tool result
// payloads BEFORE the Truncator's own trim. A 4 MiB cap covers
// realistic non-string tool returns (large list_files trees, full
// search result sets) and is far above the Truncator's default 50
// KiB tail-trim — Truncator gets a bounded input to slice down to
// the conversation limit. Earlier shape did json.Marshal(v) which
// allocates the whole tree before any trimming; a runaway tool that
// returned a 500 MB nested struct would force a 500 MB allocation
// before the runner could shrink it. The new path streams the
// encoder into a [cappedWriter] and stops the moment we cross the
// cap — bounded parent allocation regardless of result shape.
const marshalCappedLimit = 4 * 1024 * 1024

// marshalCapped renders v as JSON, capped at [marshalCappedLimit].
// On encode error, falls back to fmt.Sprint (which the truncator
// will tail-trim anyway).
func marshalCapped(v any) string {
	w := &cappedBytesWriter{limit: marshalCappedLimit}
	enc := json.NewEncoder(w)
	if err := enc.Encode(v); err != nil && !errors.Is(err, errCapReached) {
		// Unexpected encode failure (not a cap hit) — fall back to
		// Sprintf. cappedBytesWriter swallows everything past the
		// cap so the Sprintf isn't itself a memory hazard for
		// well-behaved values.
		return fmt.Sprint(v)
	}
	s := w.string()
	// json.Encoder.Encode appends a trailing newline; the Truncator
	// is line-aware and a stray empty trailing line is just noise.
	return strings.TrimRight(s, "\n")
}

// errCapReached is returned by [cappedBytesWriter.Write] once the
// cap has been hit. json.Encoder propagates the error up to the
// caller; we recognise it as the cap signal and treat the partial
// buffer as the result.
var errCapReached = errors.New("runner: marshal cap reached")

// cappedBytesWriter is an io.Writer that buffers up to limit bytes
// and returns [errCapReached] once full. Subsequent writes are
// dropped — the encoder will see the error and unwind, but the
// caller has the partial bytes available via [string].
type cappedBytesWriter struct {
	buf   []byte
	limit int // max bytes buffered; named limit, not cap, to avoid shadowing the builtin
}

func (w *cappedBytesWriter) Write(p []byte) (int, error) {
	remaining := w.limit - len(w.buf)
	if remaining <= 0 {
		return len(p), errCapReached
	}
	if len(p) <= remaining {
		w.buf = append(w.buf, p...)
		return len(p), nil
	}
	w.buf = append(w.buf, p[:remaining]...)
	return len(p), errCapReached
}

func (w *cappedBytesWriter) string() string { return string(w.buf) }
