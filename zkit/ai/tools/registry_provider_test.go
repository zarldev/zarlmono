package tools_test

import (
	"context"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type stubToolForProvider struct {
	name tools.ToolName
}

func (s stubToolForProvider) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: s.name, Description: string(s.name)}
}
func (s stubToolForProvider) Execute(context.Context, tools.ToolCall) (*tools.ToolResult, error) {
	return &tools.ToolResult{Success: true, ExecutedAt: time.Now()}, nil
}

func TestRegistry_RegisterWithProvider(t *testing.T) {
	t.Parallel()

	r := tools.NewRegistry()
	r.RegisterWithProvider(stubToolForProvider{name: "search"}, "obsidian")
	r.RegisterWithProvider(stubToolForProvider{name: "list"}, "obsidian")
	r.RegisterWithProvider(stubToolForProvider{name: "ha_state"}, "homeassistant")
	r.Register(stubToolForProvider{name: "calculator"}) // no provider

	if got := r.ProviderFor("search"); got != "obsidian" {
		t.Errorf("ProviderFor(search) = %q, want obsidian", got)
	}
	if got := r.ProviderFor("ha_state"); got != "homeassistant" {
		t.Errorf("ProviderFor(ha_state) = %q, want homeassistant", got)
	}
	if got := r.ProviderFor("calculator"); got != "" {
		t.Errorf("ProviderFor(calculator) = %q, want empty", got)
	}

	if got := r.ToolCountForProvider("obsidian"); got != 2 {
		t.Errorf("ToolCountForProvider(obsidian) = %d, want 2", got)
	}
	if got := r.ToolCountForProvider("homeassistant"); got != 1 {
		t.Errorf("ToolCountForProvider(homeassistant) = %d, want 1", got)
	}

	obsidianTools := r.ToolsByProvider("obsidian")
	if len(obsidianTools) != 2 {
		t.Errorf("ToolsByProvider(obsidian) returned %d tools, want 2", len(obsidianTools))
	}
}

func TestRegistry_UnregisterProvider(t *testing.T) {
	t.Parallel()

	r := tools.NewRegistry()
	r.RegisterWithProvider(stubToolForProvider{name: "a"}, "p1")
	r.RegisterWithProvider(stubToolForProvider{name: "b"}, "p1")
	r.RegisterWithProvider(stubToolForProvider{name: "c"}, "p2")

	versionBefore := r.Version()
	r.UnregisterProvider("p1")

	if r.Len() != 1 {
		t.Errorf("Len after UnregisterProvider = %d, want 1", r.Len())
	}
	if got := r.ToolCountForProvider("p1"); got != 0 {
		t.Errorf("p1 still has %d tools after UnregisterProvider", got)
	}
	if r.Version() <= versionBefore {
		t.Error("Version did not bump on UnregisterProvider")
	}
	if _, ok := r.Tool("c"); !ok {
		t.Error("p2 tool should still be registered")
	}
}

func TestRegistry_UnregisterProviderNoOpWhenAbsent(t *testing.T) {
	t.Parallel()

	r := tools.NewRegistry()
	r.Register(stubToolForProvider{name: "x"})
	versionBefore := r.Version()

	r.UnregisterProvider("does-not-exist")

	if r.Version() != versionBefore {
		t.Errorf("Version bumped on no-op UnregisterProvider (was %d, now %d)", versionBefore, r.Version())
	}
}

func TestRegistry_RegisterRemovesProviderTagWhenReregisteringPlain(t *testing.T) {
	t.Parallel()

	r := tools.NewRegistry()
	r.RegisterWithProvider(stubToolForProvider{name: "x"}, "old-provider")
	r.Register(stubToolForProvider{name: "x"}) // re-register without provider

	if got := r.ProviderFor("x"); got != "" {
		t.Errorf("ProviderFor(x) = %q after re-register without provider; want empty", got)
	}
	if got := r.ToolCountForProvider("old-provider"); got != 0 {
		t.Errorf("old-provider count = %d, want 0", got)
	}
}
