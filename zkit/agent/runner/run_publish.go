package runner

import (
	"context"
	"errors"
	"fmt"
	"sync"
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
	r.publishConversationEnded(ctx, spec, TerminalError, err, time.Since(start), 0, nil, terminalCause(err))
}

// --- event publishing helpers ---

func (r *Runner) publishConversationStarted(ctx context.Context, spec TaskSpec) {
	r.sink.OnConversationStarted(ctx, ConversationStarted{
		TaskID:            spec.ID,
		Depth:             spec.Depth,
		Prompt:            spec.Prompt,
		ParentToolCallID:  spec.ParentToolCallID,
		ParentExecutionID: spec.ParentExecutionID,
		AgentName:         spec.AgentName,
		Provider:          r.providerName,
		Model:             r.modelName,
	})
}

func (r *Runner) publishConversationEnded(
	ctx context.Context,
	spec TaskSpec,
	reason TerminalReason,
	err error,
	dur time.Duration,
	iterations int,
	total *llm.Usage,
	cause TerminalCause,
) {
	// Flatten to a string for the generic Error field and, in the same
	// place, pull out a structured rate-limit error so subscribers don't
	// have to re-parse the message text.
	var errStr string
	var rateLimit *llm.RateLimitError
	if err != nil {
		errStr = err.Error()
		if rle, ok := errors.AsType[*llm.RateLimitError](err); ok {
			rateLimit = rle
		}
	}
	r.sink.OnConversationEnded(ctx, ConversationEnded{
		TaskID:            spec.ID,
		Depth:             spec.Depth,
		Reason:            reason,
		Error:             errStr,
		Cause:             cause,
		RateLimit:         rateLimit,
		Duration:          dur,
		Iterations:        iterations,
		TotalUsage:        total,
		ParentToolCallID:  spec.ParentToolCallID,
		ParentExecutionID: spec.ParentExecutionID,
	})
}

func terminalCause(err error) TerminalCause {
	switch {
	case errors.Is(err, ErrStreamIdle):
		return TerminalCauseStreamIdle
	case errors.Is(err, ErrIterationTimeout):
		return TerminalCauseIterationTimeout
	case errors.Is(err, ErrCancelled):
		return TerminalCauseCaller
	default:
		return ""
	}
}

func (r *Runner) publishIterationCompleted(
	ctx context.Context,
	spec TaskSpec,
	iter int,
	delta, occupancy *llm.Usage,
	messages []llm.Message,
	toolSurface ToolSurface,
	preparationDuration, dispatchDuration time.Duration,
) {
	// The per-role breakdown is an O(history) walk + alloc; only compute it
	// when a consumer opted in via WithContextBreakdown. Otherwise Context
	// is nil and the event still carries iter + usage (what the compaction
	// gate and headless recorder actually read).
	var bd *ContextBreakdown
	if r.contextBreakdown {
		b := computeContextBreakdown(messages)
		bd = &b
	}
	r.sink.OnIterationCompleted(ctx, IterationCompleted{
		TaskID:                     spec.ID,
		Depth:                      spec.Depth,
		Iter:                       iter,
		Usage:                      occupancy,
		Delta:                      delta,
		Context:                    bd,
		ToolSurface:                toolSurface,
		RequestPreparationDuration: preparationDuration,
		ToolDispatchDuration:       dispatchDuration,
	})
}

func (r *Runner) publishContentChunk(ctx context.Context, spec TaskSpec, content string) {
	r.sink.OnContent(ctx, Content{TaskID: spec.ID, Depth: spec.Depth, Delta: content})
}

func (r *Runner) publishThinkingChunk(ctx context.Context, spec TaskSpec, thinking string) {
	r.sink.OnThinking(ctx, Thinking{TaskID: spec.ID, Depth: spec.Depth, Delta: thinking})
}

func (r *Runner) publishToolStarted(ctx context.Context, spec TaskSpec, call tools.ToolCall) {
	r.sink.OnToolStarted(ctx, ToolStarted{
		TaskID:       spec.ID,
		Depth:        spec.Depth,
		ExecutionID:  call.ExecutionID,
		ToolID:       call.ID.String(),
		ToolName:     call.ToolName.String(),
		Parameters:   tools.CloneParameters(call.Arguments),
		RawArguments: call.RawArguments,
	})
}

type nestedToolPublisher struct {
	r                 *Runner
	spec              TaskSpec
	parentExecutionID string
	mu                sync.Mutex
	nested            map[nestedExecutionKey][]string
	captureErr        error
	attempt           int
	capture           func(context.Context, ToolOutput) error
}

type nestedExecutionKey struct {
	parentID string
	childID  string
	sequence int
}

func newNestedToolPublisher(r *Runner, spec TaskSpec, parentExecutionID string) *nestedToolPublisher {
	return &nestedToolPublisher{r: r, spec: spec, parentExecutionID: parentExecutionID, nested: make(map[nestedExecutionKey][]string)}
}

func nestedKey(e tools.NestedToolCall) nestedExecutionKey {
	return nestedExecutionKey{parentID: e.ParentID.String(), childID: e.ChildID.String(), sequence: e.Sequence}
}

func (p *nestedToolPublisher) OnNestedToolStarted(ctx context.Context, e tools.NestedToolCall) {
	executionID := allocateExecutionID()
	key := nestedKey(e)
	p.mu.Lock()
	p.nested[key] = append(p.nested[key], executionID)
	p.mu.Unlock()
	p.r.publishNestedToolStarted(ctx, p.spec, e, executionID, p.parentExecutionID)
}

func (p *nestedToolPublisher) OnNestedToolFinished(ctx context.Context, e tools.NestedToolResult) {
	key := nestedKey(e.NestedToolCall)
	p.mu.Lock()
	ids := p.nested[key]
	var executionID string
	if len(ids) != 0 {
		executionID = ids[0]
		if len(ids) == 1 {
			delete(p.nested, key)
		} else {
			p.nested[key] = ids[1:]
		}
	}
	p.mu.Unlock()
	p.r.publishNestedToolFinished(ctx, p.spec, e, executionID, p.parentExecutionID)
	output := rawToolResultText(e.Result)
	success, terminalError, kind, _ := classifyNestedToolOutput(e)
	if e.Result == nil && e.Err != nil {
		output = e.Err.Error()
	}
	out := ToolOutput{
		TaskID: string(p.spec.ID), Attempt: p.attempt,
		ToolCallID: e.ChildID.String(), ToolName: e.Call.ToolName.String(),
		ExecutionID: executionID, ParentToolCallID: e.ParentID.String(),
		ParentExecutionID: p.parentExecutionID, Sequence: e.Sequence,
		Args: e.Call.RawArguments, Parameters: tools.CloneParameters(e.Call.Arguments),
		Output: output, Success: success, Error: terminalError, Kind: kind,
		Parts: resultParts(e.Result), Effects: resultEffects(e.Result),
	}
	if e.Dispatched != nil {
		out.Dispatched = new(*e.Dispatched)
	}
	var captureErr error
	if p.capture != nil {
		captureErr = p.capture(ctx, out)
	}
	if p.r.toolOutputSink != nil {
		if err := p.r.toolOutputSink.Record(ctx, out); err != nil {
			captureErr = errors.Join(captureErr, fmt.Errorf("%w: %w", ErrToolHistory, err))
		}
	}
	if captureErr != nil {
		p.mu.Lock()
		p.captureErr = errors.Join(p.captureErr, captureErr)
		p.mu.Unlock()
	}
}

func (p *nestedToolPublisher) historyError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.captureErr
}

func (p *nestedToolPublisher) executionForWait(call tools.WorkspaceWaitCall) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	ids := p.nested[nestedExecutionKey{parentID: call.ParentToolID.String(), childID: call.ToolID.String(), sequence: call.Sequence}]
	if len(ids) == 1 {
		return ids[0]
	}
	// Unknown/ambiguous observer metadata must not be attributed to the parent.
	return ""
}

func (r *Runner) publishNestedToolStarted(ctx context.Context, spec TaskSpec, e tools.NestedToolCall, executionID, parentExecutionID string) {
	r.sink.OnToolStarted(ctx, ToolStarted{
		ExecutionID: executionID, TaskID: spec.ID, Depth: spec.Depth,
		ToolID: e.ChildID.String(), ToolName: e.Call.ToolName.String(), Parameters: tools.CloneParameters(e.Call.Arguments),
		RawArguments: e.Call.RawArguments,
		ParentToolID: e.ParentID.String(), ParentExecutionID: parentExecutionID, Sequence: e.Sequence,
	})
}

func (r *Runner) publishNestedToolFinished(ctx context.Context, spec TaskSpec, e tools.NestedToolResult, executionID, parentExecutionID string) {
	effects := resultEffects(e.Result)
	success, terminalError, kind, realErr := classifyNestedToolOutput(e)
	if !success {
		errMsg := terminalError
		r.sink.OnToolFailed(ctx, ToolFailed{ExecutionID: executionID, TaskID: spec.ID, Depth: spec.Depth, ToolID: e.ChildID.String(), ToolName: e.Call.ToolName.String(), Error: errMsg, Err: realErr, Kind: kind, RawOutput: rawToolResultText(e.Result), Parts: resultParts(e.Result), Effects: effects, Duration: e.Duration, ParentToolID: e.ParentID.String(), ParentExecutionID: parentExecutionID, Sequence: e.Sequence})
		return
	}
	var data any
	if e.Result != nil {
		data = e.Result.Data
	}
	r.sink.OnToolCompleted(ctx, ToolCompleted{ExecutionID: executionID, TaskID: spec.ID, Depth: spec.Depth, ToolID: e.ChildID.String(), ToolName: e.Call.ToolName.String(), Result: data, FormattedResult: formatToolData(data), Parts: llm.CloneContentParts(e.Result.Parts), Effects: effects, Duration: e.Duration, ParentToolID: e.ParentID.String(), ParentExecutionID: parentExecutionID, Sequence: e.Sequence})
}

func (r *Runner) publishToolFinished(
	ctx context.Context,
	spec TaskSpec,
	call tools.ToolCall,
	result, raw *tools.ToolResult,
	dur time.Duration,
	execErr error,
) {
	effects := resultEffects(raw)
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
		kind := tools.Kinds.UNKNOWN
		realErr := execErr
		if result != nil && result.Err != nil {
			kind = result.Err.Kind
			realErr = result.Err
		}
		r.sink.OnToolFailed(ctx, ToolFailed{
			TaskID:      spec.ID,
			ExecutionID: call.ExecutionID,
			Depth:       spec.Depth,
			ToolID:      call.ID.String(),
			ToolName:    call.ToolName.String(),
			Duration:    dur,
			Error:       errMsg,
			Err:         realErr,
			Kind:        kind,
			Effects:     effects,
			RawOutput:   rawToolResultText(raw),
			Parts:       resultParts(raw),
		})
		return
	}
	var data any
	var parts []llm.ContentPart
	if result != nil {
		data = result.Data
		parts = llm.CloneContentParts(result.Parts)
	}
	r.sink.OnToolCompleted(ctx, ToolCompleted{
		TaskID:          spec.ID,
		ExecutionID:     call.ExecutionID,
		Depth:           spec.Depth,
		ToolID:          call.ID.String(),
		ToolName:        call.ToolName.String(),
		Result:          data,
		FormattedResult: formatToolData(data),
		Parts:           parts,
		Effects:         effects,
		Duration:        dur,
	})
}

func resultParts(result *tools.ToolResult) []llm.ContentPart {
	if result == nil {
		return nil
	}
	return llm.CloneContentParts(result.Parts)
}

func resultEffects(result *tools.ToolResult) []tools.Effect {
	if result == nil || len(result.Effects) == 0 {
		return nil
	}
	effects := append([]tools.Effect(nil), result.Effects...)
	for i := range effects {
		if effects[i].File != nil {
			file := *effects[i].File
			effects[i].File = &file
		}
		if effects[i].Process != nil {
			process := *effects[i].Process
			effects[i].Process = &process
		}
	}
	return effects
}

func (r *Runner) publishSteerInjected(ctx context.Context, spec TaskSpec, drained []llm.Message) {
	r.sink.OnSteerInjected(ctx, SteerInjected{
		TaskID:   spec.ID,
		Depth:    spec.Depth,
		Messages: drained,
	})
}

func (r *Runner) publishCompactionApplied(
	ctx context.Context,
	spec TaskSpec,
	before, after, bytesTrimmed int,
	engine string,
) {
	r.sink.OnCompactionApplied(ctx, CompactionApplied{
		TaskID:         spec.ID,
		Depth:          spec.Depth,
		MessagesBefore: before,
		MessagesAfter:  after,
		BytesTrimmed:   bytesTrimmed,
		Engine:         engine,
	})
}

func (r *Runner) publishDiagnostic(ctx context.Context, spec TaskSpec, kind, message string, attempt, limit int, backoff time.Duration, err error) {
	r.sink.OnDiagnostic(ctx, Diagnostic{
		TaskID: spec.ID, Depth: spec.Depth, Kind: kind, Message: message,
		Attempt: attempt, Limit: limit, Backoff: backoff, Err: err,
	})
}
