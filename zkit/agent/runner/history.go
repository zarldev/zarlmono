package runner

import (
	"context"
	"errors"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/options"
)

// ErrReplayHistory means canonical history or prepared request capture did not
// succeed. No subsequent provider request is made after a capture failure.
var ErrReplayHistory = errors.New("record replay history")

// ReplayMessage is a canonical occurrence independent of compaction. Message
// carries complete content and native response items. RawToolCalls retains the
// original argument bytes alongside the model-replayable normalized calls.
// Tool identifies the immutable execution occurrence and its untruncated result.
type ReplayMessage struct {
	// Kind's zero value is a legacy model-message occurrence.
	Kind ReplayOccurrenceKind `json:"kind,omitzero"`
	// Interrupted occurrences preserve partial observations, but are not model
	// conversation input: the original runner did not admit them into context.
	Interrupted  bool           `json:"interrupted,omitempty"`
	Message      llm.Message    `json:"message"`
	RawToolCalls []llm.ToolCall `json:"raw_tool_calls,omitempty"`
	Tool         *ToolOutput    `json:"tool,omitempty"`
}

// AdmittedToModelContext excludes inspection-only and interrupted observations.
func (r ReplayMessage) AdmittedToModelContext() bool {
	return r.Kind == ReplayOccurrenceKinds.MESSAGE && !r.Interrupted
}

// IsToolExecution identifies an inspection-only settled execution occurrence.
func (r ReplayMessage) IsToolExecution() bool {
	return r.Kind == ReplayOccurrenceKinds.TOOLEXECUTION
}

// recordExecution serializes nested settlement capture, independently of the
// working-message cursor. Dispatch owns and joins all callers before returning.
func (t *taskRun) recordExecution(ctx context.Context, out ToolOutput) error {
	if t.r.historySink == nil || t.spec.Depth != 0 {
		return nil
	}
	t.historyMu.Lock()
	defer t.historyMu.Unlock()
	if t.historyErr != nil {
		return t.historyErr
	}
	if err := t.r.historySink.Append(ctx, []ReplayMessage{{Kind: ReplayOccurrenceKinds.TOOLEXECUTION, Tool: &out}}); err != nil {
		t.historyErr = fmt.Errorf("%w: %w", ErrReplayHistory, err)
	}
	return t.historyErr
}

// HistorySink synchronously persists canonical append occurrences and the latest
// prepared request separately. Inputs are borrowed until the call returns;
// implementations must copy retained mutable values and return capture errors.
// Concurrent Runs may call the sink concurrently. The caller owns its lifetime.
type HistorySink interface {
	Append(context.Context, []ReplayMessage) error
	Request(context.Context, llm.CompletionRequest) error
}

// WithHistorySink captures depth-zero runs only. Recursive runs on the same
// runner must not append private child conversations to the owning session.
func WithHistorySink(sink HistorySink) options.Option[Runner] {
	return func(r *Runner) { r.historySink = sink }
}

// flushHistory runs before compaction and at settlement. Only newly appended
// occurrences enter the canonical chain; shaped requests and compaction output
// never do. The first failure is sticky to avoid retrying an ambiguous append.
func (t *taskRun) flushHistory(ctx context.Context) error {
	if t.historyErr != nil {
		return t.historyErr
	}
	if t.r.historySink == nil || t.spec.Depth != 0 {
		return nil
	}
	if t.historyOffset == len(t.messages) {
		return nil
	}
	records := make([]ReplayMessage, 0, len(t.messages)-t.historyOffset)
	for i := t.historyOffset; i < len(t.messages); i++ {
		record, ok := t.historyOverrides[i]
		if !ok {
			record.Message = t.messages[i].Clone()
		}
		records = append(records, record)
	}
	if err := t.r.historySink.Append(ctx, records); err != nil {
		t.historyErr = fmt.Errorf("%w: %w", ErrReplayHistory, err)
		return t.historyErr
	}
	t.historyOffset = len(t.messages)
	clear(t.historyOverrides)
	return nil
}

// recordInterruptedHistory preserves observed bytes and undispatched outcomes
// without contaminating the working context used by a recoverable retry.
func (t *taskRun) recordInterruptedHistory(ctx context.Context, sr streamResult, outputs []ToolOutput) error {
	if t.r.historySink == nil || t.spec.Depth != 0 {
		return nil
	}
	if err := t.flushHistory(ctx); err != nil {
		return err
	}
	if sr.content == "" && sr.thinking == "" && len(sr.continuationItems) == 0 && len(sr.toolCallOrder) == 0 {
		return nil
	}
	message := llm.Message{Role: llm.RoleAssistant, Content: sr.content, ReasoningContent: sr.thinking,
		ContentOutputIndex: sr.contentOutputIndex, ContinuationItems: sr.continuationItems}
	record := ReplayMessage{Message: message.Clone(), Interrupted: true}
	for _, id := range sr.toolCallOrder {
		call := sr.toolCalls[id].Clone()
		record.Message.ToolCalls = append(record.Message.ToolCalls, call)
		record.RawToolCalls = append(record.RawToolCalls, call.Clone())
	}
	records := []ReplayMessage{record}
	for _, output := range outputs {
		records = append(records, ReplayMessage{Interrupted: true, Tool: &output,
			Message: llm.Message{Role: llm.RoleTool, ToolCallID: output.ToolCallID, Content: "Error: " + output.Error + "\n" + output.Output}})
	}
	if err := t.r.historySink.Append(ctx, records); err != nil {
		t.historyErr = fmt.Errorf("%w: %w", ErrReplayHistory, err)
		return t.historyErr
	}
	return nil
}

func (t *taskRun) recordToolHistory(message llm.Message, d dispatchedCall, tc *llm.ToolCall, args string) {
	if t.r.historySink == nil || t.spec.Depth != 0 {
		return
	}
	success, terminalError, kind := classifyToolOutput(d.result)
	out := ToolOutput{ExecutionID: d.executionID, ToolCallID: tc.ID, ToolName: tc.Function.Name,
		TaskID: string(t.spec.ID), Attempt: t.attempt, Dispatched: new(d.dispatched),
		Args: args, Parameters: tools.CloneParameters(d.parameters),
		Output: rawToolResultText(d.rawResult), Parts: resultParts(d.rawResult), Effects: resultEffects(d.rawResult),
		Success: success, Error: terminalError, Kind: kind}
	raw := message.Clone()
	raw.Content = out.Output
	if !success && terminalError != "" {
		raw.Content = "Error: " + terminalError + "\n" + out.Output
	}
	raw.Parts = llm.CloneContentParts(out.Parts)
	t.historyOverrides[len(t.messages)-1] = ReplayMessage{Message: raw, Tool: &out}
}
