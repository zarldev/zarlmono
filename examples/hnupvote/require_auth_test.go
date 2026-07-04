package main

import (
	"context"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func authCall(name tools.ToolName) tools.ToolCall {
	return tools.ToolCall{ID: "1", ToolName: name}
}

func TestRequireAuth_BlocksGuardedWhenNotAuthed(t *testing.T) {
	g := NewRequireAuth(func(context.Context) bool { return false },
		"call hn_login first", "hn_upvote_top")
	err := g.Before(t.Context(), authCall("hn_upvote_top"))
	if err == nil {
		t.Fatal("expected guarded call to be blocked when unauthenticated")
	}
	if !strings.Contains(err.Error(), "hn_login") {
		t.Errorf("block error %q should carry the actionable hint", err)
	}
}

func TestRequireAuth_AllowsGuardedWhenAuthed(t *testing.T) {
	g := NewRequireAuth(func(context.Context) bool { return true },
		"call hn_login first", "hn_upvote_top")
	if err := g.Before(t.Context(), authCall("hn_upvote_top")); err != nil {
		t.Fatalf("authenticated guarded call should pass, got %v", err)
	}
}

func TestRequireAuth_IgnoresUnguardedTools(t *testing.T) {
	// check always fails, but hn_login itself is NOT guarded, so it must
	// be allowed — otherwise the model could never authenticate.
	g := NewRequireAuth(func(context.Context) bool { return false },
		"call hn_login first", "hn_upvote_top")
	if err := g.Before(t.Context(), authCall("hn_login")); err != nil {
		t.Fatalf("unguarded tool should never be blocked, got %v", err)
	}
}

func TestRequireAuth_NilCheckFailsClosed(t *testing.T) {
	g := NewRequireAuth(nil, "log in first", "hn_upvote_top")
	if err := g.Before(t.Context(), authCall("hn_upvote_top")); err == nil {
		t.Fatal("nil check should fail closed (block guarded calls)")
	}
}

// RequireAuth must satisfy both guardrails.Guardrail and guardrails.PreCall.
var (
	_ guardrails.Guardrail = (*RequireAuth)(nil)
	_ guardrails.PreCall   = (*RequireAuth)(nil)
)
