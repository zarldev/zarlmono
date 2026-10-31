package runner

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type toolGateKey struct{}

// WithToolGate scopes a Run — and every tool dispatch within it — to the
// tools that gate admits. A tool whose name gate rejects is hidden from
// the per-iteration LLM tool list and refused with a clear result if the
// model calls it anyway. The spawn-agent tool plants a gate on a sub-agent's
// Run ctx to enforce its work mode (e.g. explore = read-only) as real policy
// rather than prompt text. A non-nil gate replaces the inherited gate;
// a nil gate leaves the context unchanged. Use WithAdditionalToolGate to narrow
// inherited authority when delegating.
//
// The gate receives the full ToolSpec so it can filter by capability (e.g.
// Mutates) rather than just by name. The gate is read from ctx on each
// dispatch, so it scopes exactly to the Run it was planted on: a parent's
// dispatches (ctx without a gate) are unaffected, and the gate doesn't leak
// past the child Run it wraps.
func WithToolGate(ctx context.Context, gate func(tools.ToolSpec) bool) context.Context {
	if gate == nil {
		return ctx
	}
	return context.WithValue(ctx, toolGateKey{}, gate)
}

// WithAdditionalToolGate narrows the inherited tool gate by intersection.
// A nil gate preserves the inherited policy; delegation cannot use this API
// to re-enable a tool denied by an ancestor.
func WithAdditionalToolGate(ctx context.Context, gate func(tools.ToolSpec) bool) context.Context {
	if gate == nil {
		return ctx
	}
	inherited := toolGateFrom(ctx)
	if inherited == nil {
		return WithToolGate(ctx, gate)
	}
	return WithToolGate(ctx, func(spec tools.ToolSpec) bool {
		return inherited(spec) && gate(spec)
	})
}

func toolGateFrom(ctx context.Context) func(tools.ToolSpec) bool {
	gate, _ := ctx.Value(toolGateKey{}).(func(tools.ToolSpec) bool)
	return gate
}
