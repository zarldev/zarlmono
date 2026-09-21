package runner

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// ToolOutput is one captured tool result. The runner emits it before the
// truncator trims the model-facing text, so consumers can persist the full
// output for a tool-history surface.
type ToolOutput struct {
	// TaskID and Attempt identify the owning invocation and initiating provider
	// attempt. Empty/zero denotes legacy or externally produced output.
	TaskID  string `json:",omitempty"`
	Attempt int    `json:",omitempty"`
	// Dispatched distinguishes an invoked executor from a rejected/undispatched
	// observation. Nil means the producer did not record this distinction.
	Dispatched        *bool  `json:",omitempty"`
	ParentExecutionID string `json:",omitempty"`
	Sequence          int    `json:",omitempty"`
	ToolCallID        string
	ToolName          string
	Args              string // raw JSON arguments string
	Output            string // full, untruncated tool result
	Success           bool   // terminal outcome presented to the model
	Error             string // full terminal error, separate from raw Output
	Kind              tools.Kind
	Effects           []tools.Effect
	ExecutionID       string
	ParentToolCallID  string
	Parameters        tools.ToolParameters // Decoded execution parameters, not original text.
	Parts             []llm.ContentPart
}

func classifyToolOutput(result *tools.ToolResult) (bool, string, tools.Kind) {
	if result == nil {
		return false, "", tools.Kinds.UNKNOWN
	}
	kind := tools.Kinds.UNKNOWN
	if result.Err != nil {
		kind = result.Err.Kind
	}
	return result.Success, result.Error, kind
}

func classifyNestedToolOutput(e tools.NestedToolResult) (bool, string, tools.Kind, error) {
	success, terminalError, kind := classifyToolOutput(e.Result)
	var terminalErr error
	if e.Result != nil && e.Result.Err != nil {
		terminalErr = e.Result.Err
	}
	if e.Error != "" {
		success = false
		terminalError = e.Error
	}
	if e.Err != nil {
		success = false
		terminalErr = e.Err
		if e.Error == "" {
			terminalError = e.Err.Error()
		}
		kind = tools.KindOf(e.Err)
	}
	if e.Kind != tools.Kinds.UNKNOWN {
		success = false
		kind = e.Kind
	}
	return success, terminalError, kind, terminalErr
}

// ToolOutputSink receives full tool results before truncation. Implementations
// run synchronously on the dispatch path and may be called concurrently by
// parallel tools, nested tools, or runs. Inputs are borrowed until return; retain
// independently owned snapshots. A capture error terminates the turn after owned
// executions settle. Nil sink disables capture.
type ToolOutputSink interface {
	Record(ctx context.Context, out ToolOutput) error
}
