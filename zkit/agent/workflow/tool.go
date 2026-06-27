package workflow

import (
	"context"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// Tool exposes a Runnable as a zkit tool. The workflow input is the call arguments map.
type Tool struct {
	Name        tools.ToolName
	Description string
	Runnable    *Runnable
	Parameters  llm.Schema
}

// Definition describes the workflow tool.
func (t Tool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: t.Name, Description: t.Description, Parameters: t.Parameters}
}

// Execute invokes the workflow with call.Arguments.
func (t Tool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	out, err := t.Runnable.Invoke(ctx, call.Arguments)
	if err != nil {
		return nil, err
	}
	return &tools.ToolResult{ToolCallID: call.ID, Success: true, Data: out, Metadata: tools.NewToolMetadata(t.Name), ExecutedAt: time.Now()}, nil
}
