package runner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// loopState groups the per-Run counters threaded through the iteration loop.
// The four stream-recovery budgets reset after a healthy stream; correction
// budgets and one-shot latches last for the whole Run.
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
	forceCompactNoopAt     int
	toolSurfaceFingerprint string
}

// taskRun is one Run invocation's state: the immutable identity of the run
// (spec, start time, iteration cap) plus everything the loop mutates as it
// goes (history, usage accounting, recovery budgets, request policy). It
// lives on Run's stack — one value per invocation, never shared — so the
// runner stays reusable across concurrent Runs.
//
// Methods on taskRun replace the parameter trains that used to thread this
// state through every terminal builder and recovery helper: the four
// terminal exits (completed / maxedOut / cancelled / errored) and the
// stream-error ladder read it from the receiver instead of 9-element
// signatures.
type taskRun struct {
	r *Runner

	// Immutable for the life of the run.
	spec    TaskSpec
	start   time.Time
	maxIter int

	// iter is the current iteration index, stamped at the top of each
	// loop pass so the terminal builders agree with the loop position
	// without threading it through every call.
	iter int

	// messages is the working history: [system?, ...spec.Context, user
	// prompt] plus every assistant / tool / corrective turn appended as
	// the loop runs. Compaction replaces it wholesale.
	messages []llm.Message

	// finalContent is the last iteration's user-visible text — assigned
	// AFTER the text-tool-call fallback strips any tool syntax, so every
	// exit path (completed, max-iterations, stream error, cancellation)
	// reports the same cleaned content.
	finalContent string

	// lastUsage is the most recent usage observed across the run — the
	// occupancy signal the next iteration's token-pressure compaction
	// reads. Only updated when a stream actually carried usage, so it
	// never regresses to nil mid-run.
	lastUsage *llm.Usage

	// totalUsage accumulates every iteration's reported usage so the
	// terminal TaskResult / ConversationEnded event can report the full
	// token spend of the run rather than just the final iteration's
	// snapshot. Nil until the first usage-bearing iteration lands —
	// keeping it nil distinguishes "never made a call" from "made calls
	// but provider didn't report usage" (the latter would surface as
	// zeroed totals).
	totalUsage *llm.Usage
	// toolSurface is the accounting for the most recent provider request.
	toolSurface ToolSurface

	// st groups the per-run recovery budgets + the finalize-warn latch.
	st loopState

	// totalToolCalls is the cumulative tool-call count across every
	// iteration. Reported to progressUpdater after each iteration so a
	// SIGKILL'd run leaves a recoverable trail of "made it to iter N
	// with M total tool calls" in the persisted row.
	totalToolCalls int

	// thinking is the per-run request policy. It starts from the task
	// spec but TurnQuality may force it off after an empty visible
	// response — deliberately for the REMAINDER of the run, not just the
	// retry: a model that burned its budget thinking once will do it
	// again.
	thinking bool
}

// finalizeNudge returns the one request-only cap warning for this run. It marks
// the latch only when a threshold actually fires; callers must not append the
// returned text to canonical history.
func (t *taskRun) finalizeNudge(ctx context.Context) string {
	if t.st.finalizeWarned {
		return ""
	}
	cfg := t.r.finalizeWarn
	if cfg.RemainingThreshold > 0 {
		remaining := t.maxIter - t.iter
		if remaining <= cfg.RemainingThreshold {
			t.st.finalizeWarned = true
			return finalizeWarnMessage(remaining, cfg.Message)
		}
	}
	if cfg.DeadlineGrace > 0 {
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= cfg.DeadlineGrace {
			t.st.finalizeWarned = true
			return finalizeWarnTimeMessage(cfg.DeadlineGrace, cfg.Message)
		}
	}
	return ""
}

// resetStreamRecovery restores every consecutive stream-failure allowance after
// one healthy stream. Correction and finalization budgets are intentionally not
// reset: they are bounded across the whole run.
func (t *taskRun) resetStreamRecovery() {
	t.st.toolCallJSONRecovers = 0
	t.st.emptyStreamRetries = 0
	t.st.thinkingBudgetCuts = 0
	t.st.rateLimitRetries = 0
}

// applyTurnQuality applies the no-tool turn-quality correction. A true result
// means the correction was appended and the loop must retry.
func (t *taskRun) applyTurnQuality(content string, hasToolCalls bool) bool {
	if t.r.turnQuality == nil || hasToolCalls {
		return false
	}
	decision := t.r.turnQuality.Inspect(content, nil)
	if decision.Correction == "" ||
		(decision.MaxCorrections > 0 && t.st.turnQualityCorrections >= decision.MaxCorrections) {
		return false
	}
	t.st.turnQualityCorrections++
	t.messages = append(t.messages, llm.Message{Role: llm.RoleUser, Content: decision.Correction})
	if decision.DisableThinking {
		t.thinking = false
	}
	return true
}

// holdCompletion applies the no-work completion gate. A true result means a
// corrective user turn was appended and another iteration remains to act on it.
func (t *taskRun) holdCompletion(content string) bool {
	if t.r.completionGate == nil || t.iter >= t.maxIter-1 {
		return false
	}
	decision := t.r.completionGate.Inspect(t.st.mutatingCalls > 0, content)
	if decision.Correction == "" ||
		(decision.MaxCorrections > 0 && t.st.completionCorrections >= decision.MaxCorrections) {
		return false
	}
	t.st.completionCorrections++
	t.messages = append(t.messages, llm.Message{Role: llm.RoleUser, Content: decision.Correction})
	return true
}

// completed builds the TerminalCompleted result: the model emitted no more
// tool calls and the run ended on its own terms.
func (t *taskRun) completed(ctx context.Context) TaskResult {
	t.r.publishConversationEnded(ctx, t.spec, TerminalCompleted, nil, time.Since(t.start), t.iter+1, t.totalUsage, "")
	return TaskResult{
		ID:           t.spec.ID,
		Reason:       TerminalCompleted,
		Iterations:   t.iter + 1,
		Duration:     time.Since(t.start),
		FinalContent: t.finalContent,
		Messages:     stripSystem(t.messages),
		SystemPrompt: systemPromptFrom(t.messages),
		LastUsage:    t.lastUsage,
		TotalUsage:   t.totalUsage,
		ToolSurface:  t.toolSurface,
	}
}

// maxedOut builds the TerminalMaxIterations result: the loop exhausted its
// iteration cap without a terminal condition.
func (t *taskRun) maxedOut(ctx context.Context) TaskResult {
	t.r.publishConversationEnded(ctx, t.spec, TerminalMaxIterations, nil, time.Since(t.start), t.maxIter, t.totalUsage, "")
	return TaskResult{
		ID:           t.spec.ID,
		Reason:       TerminalMaxIterations,
		Iterations:   t.maxIter,
		Duration:     time.Since(t.start),
		FinalContent: t.finalContent,
		Messages:     stripSystem(t.messages),
		SystemPrompt: systemPromptFrom(t.messages),
		LastUsage:    t.lastUsage,
		TotalUsage:   t.totalUsage,
		ToolSurface:  t.toolSurface,
	}
}

// cancelled builds the common TerminalCancelled result shared by every
// cancellation path: the top-of-loop ctx.Err and conversation-lock checks,
// the backoff-cancel, and the cancelled mid-stream drain. Iterations is the
// count that fully completed (the cancelled one didn't), matching the
// top-of-loop checks — so all cancel paths agree. The caller decides whether
// cancelErr is wrapped (e.g. with ErrCancelled) before passing.
func (t *taskRun) cancelled(ctx context.Context, cancelErr error) TaskResult {
	// Detach cancellation but keep ctx's values (trace IDs, task metadata)
	// so the terminal event carries the run's context — the publish path
	// is cancellation-driven, so the original ctx is already Done. A sink
	// that honors ctx cancellation would otherwise drop this event.
	cause := terminalCause(cancelErr)
	t.r.publishConversationEnded(context.WithoutCancel(ctx), t.spec, TerminalCancelled, nil, time.Since(t.start), t.iter, t.totalUsage, cause)
	return TaskResult{
		ID:           t.spec.ID,
		Reason:       TerminalCancelled,
		Iterations:   t.iter,
		Duration:     time.Since(t.start),
		Cause:        cause,
		FinalContent: t.finalContent,
		Messages:     stripSystem(t.messages),
		SystemPrompt: systemPromptFrom(t.messages),
		LastUsage:    t.lastUsage,
		TotalUsage:   t.totalUsage,
		ToolSurface:  t.toolSurface,
		Err:          cancelErr,
	}
}

// errored builds the TerminalError result for a non-recoverable error path
// and emits ConversationEnded(Reason=error) so subscribers — especially
// TUIs that may have orphan tool rows or "↳ sub-agent starting" markers
// waiting for closure — see a clean end-of-turn signal even on the error
// path. Unlike the other exits it leaves SystemPrompt empty (long-standing
// shape; consumers of error results read Err, not the prompt).
func (t *taskRun) errored(ctx context.Context, err error) TaskResult {
	cause := terminalCause(err)
	t.r.publishConversationEnded(ctx, t.spec, TerminalError, err, time.Since(t.start), t.iter+1, t.totalUsage, cause)
	return TaskResult{
		ID:           t.spec.ID,
		Reason:       TerminalError,
		Iterations:   t.iter + 1,
		Duration:     time.Since(t.start),
		Cause:        cause,
		FinalContent: t.finalContent,
		Messages:     stripSystem(t.messages),
		LastUsage:    t.lastUsage,
		TotalUsage:   t.totalUsage,
		ToolSurface:  t.toolSurface,
		Err:          err,
	}
}

// recoverStreamErr classifies a failed completion stream into a terminal
// result or a retry, mutating the soft-recovery budgets (and, for the
// corrective-message case, the history) on the receiver. It is the loop's
// post-drain error ladder, extracted so the loop body reads as one branch
// ("stream failed → recover or terminate") instead of three
// differently-shaped inline cases. A nil return means retry: continue the
// loop with the possibly-updated t.messages.
//
// The cases, in order:
//   - ErrCancelled: terminal (the caller's ctx unwound mid-stream).
//   - ErrEmptyStream under budget: the provider opened the stream then cut
//     it empty (slow prefill); re-issue the identical request after an
//     exponential backoff — no corrective message. Cancellation during the
//     backoff is itself terminal.
//   - upstream malformed-JSON under budget: inject a corrective user turn so
//     the model re-emits with valid escaping, and retry.
//   - otherwise: terminal, wrapping streamErr with the failed iteration.
func (t *taskRun) recoverStreamErr(ctx context.Context, streamErr error, accepted bool) *TaskResult {
	if errors.Is(streamErr, ErrCancelled) {
		tr := t.cancelled(ctx, streamErr)
		return &tr
	}
	var rle *llm.RateLimitError
	if !accepted && errors.As(streamErr, &rle) && rle.Retryable && !rle.Permanent && t.st.rateLimitRetries < rateLimitRetryLimit {
		t.st.rateLimitRetries++
		backoff := rle.RetryAfter
		if backoff <= 0 {
			backoff = 5 * time.Second
		}
		t.r.publishDiagnostic(t.spec, "rate_limit_retry", "provider rate limited; retrying", t.st.rateLimitRetries, rateLimitRetryLimit, backoff, streamErr)
		select {
		case <-ctx.Done():
			tr := t.cancelled(ctx, fmt.Errorf("%w: %w", ErrCancelled, context.Cause(ctx)))
			return &tr
		case <-time.After(backoff):
		}
		return nil
	}

	if !accepted && errors.Is(streamErr, ErrEmptyStream) && t.st.emptyStreamRetries < emptyStreamRetryLimit {
		t.st.emptyStreamRetries++
		backoff := t.r.emptyStreamBackoff << (t.st.emptyStreamRetries - 1)
		t.r.publishDiagnostic(t.spec, "empty_stream_retry", "provider returned an empty stream; retrying", t.st.emptyStreamRetries, emptyStreamRetryLimit, backoff, streamErr)
		if backoff > 0 {
			select {
			case <-ctx.Done():
				tr := t.cancelled(ctx, fmt.Errorf("%w: %w", ErrCancelled, context.Cause(ctx)))
				return &tr
			case <-time.After(backoff):
			}
		}
		return nil
	}

	if !accepted && errors.Is(streamErr, ErrThinkingBudget) && t.st.thinkingBudgetCuts < thinkingBudgetRecoverLimit {
		t.st.thinkingBudgetCuts++
		t.r.publishDiagnostic(t.spec, "thinking_budget_recovery", "thinking budget exceeded; injecting correction", t.st.thinkingBudgetCuts, thinkingBudgetRecoverLimit, 0, streamErr)
		t.messages = append(t.messages, llm.Message{
			Role:    llm.RoleUser,
			Content: thinkingBudgetRecoveryMessage,
		})
		return nil
	}

	if !accepted && isUpstreamToolCallJSONError(streamErr) && t.st.toolCallJSONRecovers < toolCallJSONRecoverLimit {
		t.st.toolCallJSONRecovers++
		t.r.publishDiagnostic(t.spec, "tool_call_json_recovery", "upstream rejected tool-call JSON; injecting correction", t.st.toolCallJSONRecovers, toolCallJSONRecoverLimit, 0, streamErr)
		t.messages = append(t.messages, llm.Message{
			Role:    llm.RoleUser,
			Content: upstreamToolCallJSONRecoveryMessage,
		})
		return nil
	}

	// streamErr already carries its own context — the openai-family
	// providers prefix "stream:", and the drain tags sentinels
	// (ErrEmptyStream, ErrStreamIdle, ...). Wrap with the iteration that
	// failed rather than a second generic "stream:" label, which only
	// produced "stream: stream: …".
	tr := t.errored(ctx, fmt.Errorf("iteration %d: %w", t.iter, streamErr))
	return &tr
}

// maybeCompact applies the auto-compaction policy to the working history,
// replacing t.messages when the engine trims. See autocompact.go for the
// policy itself; this is the taskRun-side seam.
func (t *taskRun) maybeCompact(ctx context.Context) error {
	msgs, err := t.r.maybeCompact(ctx, t.spec, t.messages, t.lastUsage, t.iter, &t.st)
	if err != nil {
		return err
	}
	t.messages = msgs
	return nil
}
