package main

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// AuthCheck reports whether the session is currently authenticated. It's
// consulted before each guarded tool call.
type AuthCheck func(ctx context.Context) bool

// RequireAuth is a PreCall guardrail that blocks a set of effectful tools
// until AuthCheck passes, returning a tool-failure that steers the model
// to authenticate first (e.g. "call hn_login before hn_upvote").
//
// It's specific to this example: the gate observes the example session's
// login state (whatever the AuthCheck closes over — here a logged-in
// flag), so "are we allowed to act yet?" lives here rather than inside
// the tool. That keeps the actuator a dumb click-and-report primitive and
// applies the rule uniformly to every guarded action: add hn_comment /
// hn_flag to the guarded set and they inherit the same login gate for
// free.
//
// Because it runs in Before, a blocked call never dispatches; the model
// sees the steer as a failed tool result and (per the prompt) calls the
// login tool next.
type RequireAuth struct {
	check   AuthCheck
	hint    string
	guarded map[tools.ToolName]struct{}
}

// NewRequireAuth guards the named tools behind check. hint is the steer
// surfaced when a guarded call is blocked while unauthenticated — make it
// actionable (name the login tool). A nil check blocks every guarded call
// (fail-closed), which is the safe default if the caller forgot to wire
// authentication.
func NewRequireAuth(check AuthCheck, hint string, guarded ...tools.ToolName) *RequireAuth {
	g := &RequireAuth{
		check:   check,
		hint:    hint,
		guarded: make(map[tools.ToolName]struct{}, len(guarded)),
	}
	for _, n := range guarded {
		g.guarded[n] = struct{}{}
	}
	return g
}

// Name implements guardrails.Guardrail.
func (g *RequireAuth) Name() string { return "require_auth" }

// Before implements guardrails.PreCall. It blocks a guarded call when
// AuthCheck fails; unguarded tools and the authenticated path return nil
// so dispatch proceeds. The returned tools.Validation error is wrapped by
// the runner into a failed ToolResult the model reads.
func (g *RequireAuth) Before(ctx context.Context, call tools.ToolCall) error {
	if _, guarded := g.guarded[call.ToolName]; !guarded {
		return nil
	}
	if g.check != nil && g.check(ctx) {
		return nil
	}
	return tools.Validation(call.ToolName.String(), g.hint)
}
