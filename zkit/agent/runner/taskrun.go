package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

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

// completed builds the TerminalCompleted result: the model emitted no more
// tool calls and the run ended on its own terms.
func (t *taskRun) completed(ctx context.Context) TaskResult {
	t.r.publishConversationEnded(ctx, t.spec, TerminalCompleted, "", time.Since(t.start), t.iter+1, t.totalUsage)
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
	}
}

// maxedOut builds the TerminalMaxIterations result: the loop exhausted its
// iteration cap without a terminal condition.
func (t *taskRun) maxedOut(ctx context.Context) TaskResult {
	t.r.publishConversationEnded(ctx, t.spec, TerminalMaxIterations, "", time.Since(t.start), t.maxIter, t.totalUsage)
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
	t.r.publishConversationEnded(context.WithoutCancel(ctx), t.spec, TerminalCancelled, "", time.Since(t.start), t.iter, t.totalUsage)
	return TaskResult{
		ID:           t.spec.ID,
		Reason:       TerminalCancelled,
		Iterations:   t.iter,
		Duration:     time.Since(t.start),
		FinalContent: t.finalContent,
		Messages:     stripSystem(t.messages),
		SystemPrompt: systemPromptFrom(t.messages),
		LastUsage:    t.lastUsage,
		TotalUsage:   t.totalUsage,
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
	t.r.publishConversationEnded(ctx, t.spec, TerminalError, err.Error(), time.Since(t.start), t.iter+1, t.totalUsage)
	return TaskResult{
		ID:           t.spec.ID,
		Reason:       TerminalError,
		Iterations:   t.iter + 1,
		Duration:     time.Since(t.start),
		FinalContent: t.finalContent,
		Messages:     stripSystem(t.messages),
		LastUsage:    t.lastUsage,
		TotalUsage:   t.totalUsage,
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
func (t *taskRun) recoverStreamErr(ctx context.Context, streamErr error) *TaskResult {
	if errors.Is(streamErr, ErrCancelled) {
		tr := t.cancelled(ctx, streamErr)
		return &tr
	}

	if errors.Is(streamErr, ErrEmptyStream) && t.st.emptyStreamRetries < emptyStreamRetryLimit {
		t.st.emptyStreamRetries++
		backoff := t.r.emptyStreamBackoff << (t.st.emptyStreamRetries - 1)
		slog.WarnContext(ctx, "runner: empty stream from provider, retrying",
			"task", string(t.spec.ID), "iter", t.iter,
			"retry_attempt", t.st.emptyStreamRetries,
			"limit", emptyStreamRetryLimit,
			"backoff_ms", backoff.Milliseconds(),
			"err", streamErr,
		)
		if backoff > 0 {
			select {
			case <-ctx.Done():
				tr := t.cancelled(ctx, fmt.Errorf("%w: %w", ErrCancelled, ctx.Err()))
				return &tr
			case <-time.After(backoff):
			}
		}
		return nil
	}

	if errors.Is(streamErr, ErrThinkingBudget) && t.st.thinkingBudgetCuts < thinkingBudgetRecoverLimit {
		t.st.thinkingBudgetCuts++
		slog.WarnContext(ctx, "runner: thinking-only budget exceeded, injecting corrective message",
			"task", string(t.spec.ID), "iter", t.iter,
			"recover_attempt", t.st.thinkingBudgetCuts,
			"limit", thinkingBudgetRecoverLimit,
			"err", streamErr,
		)
		t.messages = append(t.messages, llm.Message{
			Role:    llm.RoleUser,
			Content: thinkingBudgetRecoveryMessage,
		})
		return nil
	}

	if isUpstreamToolCallJSONError(streamErr) && t.st.toolCallJSONRecovers < toolCallJSONRecoverLimit {
		t.st.toolCallJSONRecovers++
		slog.WarnContext(ctx, "runner: upstream rejected tool-call JSON, injecting corrective message",
			"task", string(t.spec.ID), "iter", t.iter,
			"recover_attempt", t.st.toolCallJSONRecovers,
			"limit", toolCallJSONRecoverLimit,
			"err", streamErr,
		)
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
