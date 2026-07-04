package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/ai/llm/repair"
)

// DecodeArgs decodes a ToolParameters map into T via a JSON round-trip through
// repair.Unmarshal, so small-model quirks (literal newlines, trailing commas,
// missing closers) get repaired at the decode boundary. Returns a *Error of
// Kinds.VALIDATION on failure; callers pass it straight to Failure.
func DecodeArgs[T any](params ToolParameters) (T, error) {
	var out T
	raw, err := json.Marshal(params)
	if err != nil {
		return out, Validation("decode", fmt.Sprintf("re-encode arguments: %v", err))
	}
	if err := repair.Unmarshal(raw, &out); err != nil {
		return out, Validation("decode", fmt.Sprintf(
			"tool arguments did not decode into the expected struct: %v. "+
				"Check field names and types against the tool's JSON Schema.", err))
	}
	return out, nil
}

// TypedHandler is the business logic for a typed tool. Args is decoded from
// the model-provided tool-call arguments, and Result is stored directly in the
// ToolResult data field. Use exported fields with json tags on Args and Result
// so the LLM-facing schema and the runtime decoder agree.
type TypedHandler[Args any, Result any] func(context.Context, Args) (Result, error)

// TypedOption customizes a typed tool adapter for a specific Result type.
type TypedOption[Result any] func(*typedOptions[Result])

type typedOptions[Result any] struct {
	effects func(Result) []Effect
}

// WithTypedEffects derives result effects from a typed result. This keeps the
// main handler typed while still letting file/process tools emit structured
// side-effect facts for guardrails and audit views.
func WithTypedEffects[Result any](fn func(Result) []Effect) TypedOption[Result] {
	return func(o *typedOptions[Result]) {
		o.effects = fn
	}
}

type typedTool[Args any, Result any] struct {
	spec    ToolSpec
	handler TypedHandler[Args, Result]
	options typedOptions[Result]
}

// NewTyped adapts typed tool business logic to the existing Tool interface.
// The adapter is intentionally a boundary: it decodes the raw ToolParameters
// map once, runs typed code, and returns a typed result payload. Existing
// registries, runners, guardrails, and providers continue to see a normal Tool.
func NewTyped[Args any, Result any](spec ToolSpec, handler TypedHandler[Args, Result], opts ...TypedOption[Result]) Tool {
	options := typedOptions[Result]{}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	return typedTool[Args, Result]{spec: spec, handler: handler, options: options}
}

// Definition returns the LLM-facing tool specification supplied to NewTyped.
func (t typedTool[Args, Result]) Definition() ToolSpec { return t.spec }

// Execute decodes call arguments into Args, invokes the typed handler, and
// packages the typed result into the standard ToolResult envelope.
func (t typedTool[Args, Result]) Execute(ctx context.Context, call ToolCall) (*ToolResult, error) {
	args, err := DecodeArgs[Args](call.Arguments)
	if err != nil {
		return Failure(call.ID, err), nil
	}
	result, err := t.handler(ctx, args)
	if err != nil {
		return Failure(call.ID, err), nil
	}
	var effects []Effect
	if t.options.effects != nil {
		effects = t.options.effects(result)
	}
	return Success(call.ID, result, effects...), nil
}
