package runner

import "context"

// ToolOutput is one captured tool result. The runner emits it before the
// truncator trims the model-facing text, so consumers can persist the full
// output for a tool-history surface.
type ToolOutput struct {
	ToolCallID string
	ToolName   string
	Args       string // raw JSON arguments string
	Output     string // full, untruncated tool result
}

// ToolOutputSink receives full tool results before truncation. Implementations
// run synchronously on the runner goroutine and should be fast (a single
// INSERT or channel send), never blocking network calls. Nil sink disables
// capture.
type ToolOutputSink interface {
	Record(ctx context.Context, out ToolOutput)
}
