package service

import (
	"fmt"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

// ToolResultText renders a shared tool result as the text the LLM/transcript
// sees. Tools carry their human-facing payload as a string in Data on
// success; failures surface the Error string. Non-string Data is formatted
// best-effort.
func ToolResultText(r *tools.ToolResult) string {
	if r == nil {
		return ""
	}
	if !r.Success {
		return r.Error
	}
	switch d := r.Data.(type) {
	case nil:
		return ""
	case string:
		return d
	default:
		return fmt.Sprint(d)
	}
}

// LLMToolFromSpec converts a single shared tools.ToolSpec into the llm.Tool
// shape the chat providers consume.
func LLMToolFromSpec(s tools.ToolSpec) llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name:        s.Name.String(),
			Description: s.Description,
			Parameters:  s.Parameters,
		},
	}
}

// LLMToolSpecs renders a tool registry's specs as LLM tool definitions,
// skipping any tool whose name appears in exclude (nil excludes nothing).
// Description overrides applied by the registry are preserved.
func LLMToolSpecs(r *tools.Registry, exclude map[string]bool) []llm.Tool {
	specs := r.ToolSpecs()
	out := make([]llm.Tool, 0, len(specs))
	for _, s := range specs {
		if exclude[s.Name.String()] {
			continue
		}
		out = append(out, LLMToolFromSpec(s))
	}
	return out
}
