package guardrails

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// NonEmptyResultGuardrail rejects successful tool results whose Data
// is nil, empty string, or whitespace. Tools that legitimately return
// "nothing to say" (a poll with no new entries, an idempotent write)
// should set Data to a sentinel like "ok" rather than leaving it nil —
// the LLM otherwise sees a blank tool message and tends to retry blindly.
//
// Errors and non-Success results pass through unchanged; this guardrail
// only catches the "successful but vacuous" shape.
type NonEmptyResultGuardrail struct {
	// Tools, when non-empty, scopes the check to the named tools.
	// Empty matches every tool.
	Tools []tools.ToolName
}

// Name returns the guardrail's identifier.
func (g *NonEmptyResultGuardrail) Name() string { return "nonempty_result" }

// Inspect rejects (result.Success && empty Data).
func (g *NonEmptyResultGuardrail) Inspect(
	_ context.Context,
	call tools.ToolCall,
	result *tools.ToolResult,
	dispatchErr error,
) error {
	if !successfulResult(result, dispatchErr) {
		return nil
	}
	if !g.matches(call.ToolName) {
		return nil
	}
	if isEmptyData(result.Data) {
		return errors.New("tool returned no data on success")
	}
	return nil
}

func (g *NonEmptyResultGuardrail) matches(name tools.ToolName) bool {
	if len(g.Tools) == 0 {
		return true
	}
	return slices.Contains(g.Tools, name)
}

func isEmptyData(data any) bool {
	if data == nil {
		return true
	}
	if s, ok := data.(string); ok {
		return strings.TrimSpace(s) == ""
	}
	return false
}

// GuardrailFunc adapts a plain function into a Guardrail. Useful for
// one-off validators that don't warrant a named type — e.g. an inline
// "result must be a map with key X" check in a consumer's wiring code.
type GuardrailFunc struct {
	GuardName string
	Fn        func(ctx context.Context, call tools.ToolCall, result *tools.ToolResult, execErr error) error
}

// Name returns the configured guardrail identifier. Falls back to
// "anonymous" if unset so log messages remain readable.
func (g GuardrailFunc) Name() string {
	if g.GuardName == "" {
		return "anonymous"
	}
	return g.GuardName
}

// Inspect delegates to the configured function. A nil Fn is treated
// as a pass-through.
func (g GuardrailFunc) Inspect(
	ctx context.Context,
	call tools.ToolCall,
	result *tools.ToolResult,
	execErr error,
) error {
	if g.Fn == nil {
		return nil
	}
	return g.Fn(ctx, call, result, execErr)
}
