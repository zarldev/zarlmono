package runner

import (
	"context"
	"errors"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// publishSetupFailed reports an error raised during Run setup (e.g.
// PromptSource.System rendering the system prompt) that aborts before the
// main loop. Without an event the failure would have none at all — a
// consumer reacting to the stream rather than the returned error
// (zarlcode's conversation wrapper) would see the turn vanish silently.
//
// It emits Started THEN Ended so the bookend pair invariant holds: a
// consumer pairing Started/Ended (e.g. an in-flight counter) stays
// balanced, and the TUI renders the prompt before the error rather than a
// bare toast for a turn that never appeared to start.
func (r *Runner) publishSetupFailed(ctx context.Context, spec TaskSpec, start time.Time, err error) {
	r.publishConversationStarted(ctx, spec)
	r.publishConversationEnded(ctx, spec, TerminalError, err.Error(), time.Since(start), 0, nil)
}

// --- event publishing helpers ---

func (r *Runner) publishConversationStarted(_ context.Context, spec TaskSpec) {
	if r.sink == nil {
		return
	}
	r.sink.OnConversationStarted(ConversationStarted{
		TaskID:           spec.ID,
		Depth:            spec.Depth,
		Prompt:           spec.Prompt,
		ParentToolCallID: spec.ParentToolCallID,
		AgentName:        spec.AgentName,
	})
}

func (r *Runner) publishConversationEnded(
	_ context.Context,
	spec TaskSpec,
	reason TerminalReason,
	errStr string,
	dur time.Duration,
	iterations int,
	total *llm.Usage,
) {
	if r.sink == nil {
		return
	}
	r.sink.OnConversationEnded(ConversationEnded{
		TaskID:           spec.ID,
		Depth:            spec.Depth,
		Reason:           reason,
		Error:            errStr,
		Duration:         dur,
		Iterations:       iterations,
		TotalUsage:       total,
		ParentToolCallID: spec.ParentToolCallID,
	})
}

func (r *Runner) publishIterationCompleted(_ context.Context, spec TaskSpec, iter int, delta, occupancy *llm.Usage, messages []llm.Message) {
	if r.sink == nil {
		return
	}
	// The per-role breakdown is an O(history) walk + alloc; only compute it
	// when a consumer opted in via WithContextBreakdown. Otherwise Context
	// is nil and the event still carries iter + usage (what the compaction
	// gate and headless recorder actually read).
	var bd *ContextBreakdown
	if r.contextBreakdown {
		b := computeContextBreakdown(messages)
		bd = &b
	}
	r.sink.OnIterationCompleted(IterationCompleted{
		TaskID:  spec.ID,
		Depth:   spec.Depth,
		Iter:    iter,
		Usage:   occupancy,
		Delta:   delta,
		Context: bd,
	})
}

func (r *Runner) publishContentChunk(_ context.Context, spec TaskSpec, content string) {
	if r.sink == nil {
		return
	}
	r.sink.OnContent(Content{TaskID: spec.ID, Depth: spec.Depth, Delta: content})
}

func (r *Runner) publishThinkingChunk(_ context.Context, spec TaskSpec, thinking string) {
	if r.sink == nil {
		return
	}
	r.sink.OnThinking(Thinking{TaskID: spec.ID, Depth: spec.Depth, Delta: thinking})
}

func (r *Runner) publishToolStarted(_ context.Context, spec TaskSpec, call tools.ToolCall) {
	if r.sink == nil {
		return
	}
	r.sink.OnToolStarted(ToolStarted{
		TaskID:     spec.ID,
		Depth:      spec.Depth,
		ToolID:     call.ID,
		ToolName:   call.ToolName.String(),
		Parameters: call.Arguments,
	})
}

func (r *Runner) publishToolFinished(
	_ context.Context,
	spec TaskSpec,
	call tools.ToolCall,
	result *tools.ToolResult,
	dur time.Duration,
	execErr error,
	abandoned bool,
) {
	if r.sink == nil {
		return
	}
	effects := resultEffects(result)
	if execErr != nil || (result != nil && !result.Success) {
		errMsg := ""
		if execErr != nil {
			// User-initiated cancel propagates as ctx.Canceled (or
			// DeadlineExceeded for tool-level timeouts) through every
			// in-flight tool. Surface it as a terse "(cancelled)" so
			// the consumer renders one cancel line per tool instead
			// of "ERROR: context canceled" which reads like a fault.
			switch {
			case errors.Is(execErr, context.Canceled):
				errMsg = "(cancelled)"
			case errors.Is(execErr, context.DeadlineExceeded):
				errMsg = "(timed out)"
			default:
				errMsg = execErr.Error()
			}
		} else if result != nil {
			errMsg = result.Error
		}
		// Carry the underlying typed error for sinks (logging / introspection):
		// the result's *tools.Error when present, otherwise the exec error
		// (cancel / timeout). The UI consumes only errMsg + kind, so this
		// detail never reaches the transcript.
		var kind tools.Kind
		realErr := execErr
		if result != nil {
			if result.Err != nil {
				kind = result.Err.Kind
				realErr = result.Err
			}
		}
		r.sink.OnToolFailed(ToolFailed{
			TaskID:    spec.ID,
			Depth:     spec.Depth,
			ToolID:    call.ID,
			ToolName:  call.ToolName.String(),
			Duration:  dur,
			Error:     errMsg,
			Err:       realErr,
			Kind:      kind,
			Abandoned: abandoned,
			Effects:   effects,
		})
		return
	}
	var data any
	if result != nil {
		data = result.Data
	}
	r.sink.OnToolCompleted(ToolCompleted{
		TaskID:          spec.ID,
		Depth:           spec.Depth,
		ToolID:          call.ID,
		ToolName:        call.ToolName.String(),
		Result:          data,
		FormattedResult: formatToolData(data),
		Effects:         effects,
		Duration:        dur,
	})
}

func resultEffects(result *tools.ToolResult) []tools.Effect {
	if result == nil || len(result.Effects) == 0 {
		return nil
	}
	return append([]tools.Effect(nil), result.Effects...)
}

func (r *Runner) publishSteerInjected(_ context.Context, spec TaskSpec, drained []llm.Message) {
	if r.sink == nil {
		return
	}
	r.sink.OnSteerInjected(SteerInjected{
		TaskID:   spec.ID,
		Depth:    spec.Depth,
		Messages: drained,
	})
}

func (r *Runner) publishCompactionApplied(
	_ context.Context,
	spec TaskSpec,
	before, after, bytesTrimmed int,
	engine string,
) {
	if r.sink == nil {
		return
	}
	r.sink.OnCompactionApplied(CompactionApplied{
		TaskID:         spec.ID,
		Depth:          spec.Depth,
		MessagesBefore: before,
		MessagesAfter:  after,
		BytesTrimmed:   bytesTrimmed,
		Engine:         engine,
	})
}
