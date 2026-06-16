package guardrails

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// SchemaGuardrail validates a tool call's arguments against the
// tool's declared JSON Schema (ToolSpec.Parameters) before dispatch.
// Catches missing required fields, wrong types, and out-of-enum
// values before the tool's own Execute has to.
//
// Rejections carry tools.Validation errors, so failed ToolResults
// classify as Kind=Kinds.VALIDATION. Calls to unknown tools (the
// Iterable doesn't yield them) pass through — the inner source
// surfaces its own "tool not found" error.
type SchemaGuardrail struct {
	iter tools.Iterable
}

// NewSchemaGuardrail builds a SchemaGuardrail that looks up specs
// through src on every call. Snapshot-on-call means tools registered
// dynamically (the agent built one via `register`, MCP just connected)
// become validated as soon as they appear, without restarting the
// guardrail.
func NewSchemaGuardrail(src tools.Iterable) *SchemaGuardrail {
	return &SchemaGuardrail{iter: src}
}

// Name returns the guardrail's identifier.
func (g *SchemaGuardrail) Name() string { return "schema" }

// Before validates call.Arguments against the registered tool's
// schema. Returns a tools.Validation error on rejection.
func (g *SchemaGuardrail) Before(ctx context.Context, call tools.ToolCall) error {
	spec, ok := g.findSpec(ctx, call.ToolName)
	if !ok {
		return nil
	}
	if spec.Parameters.IsZero() {
		return nil
	}
	if err := validateJSONSchema(spec.Parameters, map[string]any(call.Arguments)); err != nil {
		return tools.Validation(call.ToolName.String(), err.Error())
	}
	return nil
}

func (g *SchemaGuardrail) findSpec(ctx context.Context, name tools.ToolName) (tools.ToolSpec, bool) {
	for t := range g.iter.Tools(ctx) {
		s := t.Definition()
		if s.Name == name {
			return s, true
		}
	}
	return tools.ToolSpec{}, false
}
