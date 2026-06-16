package tools_test

import (
	"context"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type stubTool struct {
	name        tools.ToolName
	description string
}

func (s stubTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: s.name, Description: s.description}
}

func (s stubTool) Execute(context.Context, tools.ToolCall) (*tools.ToolResult, error) {
	return &tools.ToolResult{Success: true, ExecutedAt: time.Now()}, nil
}

func TestMemoryDescriptionStore_LiveOverride(t *testing.T) {
	t.Parallel()

	store := tools.NewMemoryDescriptionStore()
	tool := stubTool{name: "x", description: "default"}
	wrapped := tools.WrapDescriptionOverrides([]tools.Tool{tool}, store)[0]

	if got := wrapped.Definition().Description; got != "default" {
		t.Fatalf("pre-override description = %q, want %q", got, "default")
	}

	store.Set("x", "override-1")
	if got := wrapped.Definition().Description; got != "override-1" {
		t.Errorf("after set, description = %q, want %q", got, "override-1")
	}

	store.Set("x", "override-2")
	if got := wrapped.Definition().Description; got != "override-2" {
		t.Errorf("after re-set, description = %q, want %q", got, "override-2")
	}

	store.Delete("x")
	if got := wrapped.Definition().Description; got != "default" {
		t.Errorf("after delete, description = %q, want %q", got, "default")
	}
}

type recordingBumper struct{ bumps int }

func (r *recordingBumper) BumpVersion() { r.bumps++ }

func TestMemoryDescriptionStore_BumpsOnWrite(t *testing.T) {
	t.Parallel()

	store := tools.NewMemoryDescriptionStore()
	b := &recordingBumper{}
	store.AddBumper(b)

	store.Set("x", "a")
	if b.bumps != 1 {
		t.Errorf("bumps after set = %d, want 1", b.bumps)
	}
	store.Set("x", "b")
	if b.bumps != 2 {
		t.Errorf("bumps after 2nd set = %d, want 2", b.bumps)
	}
	store.Delete("x")
	if b.bumps != 3 {
		t.Errorf("bumps after delete = %d, want 3", b.bumps)
	}
	store.Delete("x") // absent now
	if b.bumps != 3 {
		t.Errorf("bumps after no-op delete = %d, want 3 (unchanged)", b.bumps)
	}
	store.Load(map[tools.ToolName]string{"p": "1", "q": "2", "r": "3"})
	if b.bumps != 4 {
		t.Errorf("bumps after load = %d, want 4", b.bumps)
	}
}

func TestRegistry_DescriptionOverrideAppliesToToolSpecs(t *testing.T) {
	t.Parallel()

	store := tools.NewMemoryDescriptionStore()
	reg := tools.NewRegistry()
	reg.SetDescriptionStore(store)
	store.AddBumper(reg)

	reg.Register(stubTool{name: "foo", description: "default-foo"})

	specs := reg.ToolSpecs()
	if len(specs) != 1 || specs[0].Description != "default-foo" {
		t.Fatalf("pre-override specs = %+v, want default-foo", specs)
	}

	versionBefore := reg.Version()
	store.Set("foo", "custom-foo")
	if reg.Version() <= versionBefore {
		t.Error("Registry.Version did not bump on description override write")
	}

	specs = reg.ToolSpecs()
	if specs[0].Description != "custom-foo" {
		t.Errorf("post-override description = %q, want custom-foo", specs[0].Description)
	}
}

func TestRegistry_DescriptionOverrideAppliesToTools(t *testing.T) {
	t.Parallel()

	store := tools.NewMemoryDescriptionStore()
	reg := tools.NewRegistry()
	reg.SetDescriptionStore(store)
	reg.Register(stubTool{name: "foo", description: "default-foo"})

	store.Set("foo", "custom-foo")
	var got string
	for tool := range reg.Tools(t.Context()) {
		if tool.Definition().Name == "foo" {
			got = tool.Definition().Description
		}
	}
	if got != "custom-foo" {
		t.Fatalf("Tools description = %q, want custom-foo", got)
	}
}

func TestUnwrapDescriptionOverride(t *testing.T) {
	t.Parallel()

	store := tools.NewMemoryDescriptionStore()
	store.Set("x", "overridden")
	original := stubTool{name: "x", description: "default"}
	wrapped := tools.WrapDescriptionOverrides([]tools.Tool{original}, store)[0]

	if got := wrapped.Definition().Description; got != "overridden" {
		t.Errorf("wrapped description = %q, want overridden", got)
	}

	unwrapped := tools.UnwrapDescriptionOverride(wrapped)
	if got := unwrapped.Definition().Description; got != "default" {
		t.Errorf("unwrapped description = %q, want default", got)
	}

	// UnwrapDescriptionOverride is a no-op on an unwrapped tool.
	if got := tools.UnwrapDescriptionOverride(original); got.Definition().Description != "default" {
		t.Errorf("unwrap of plain tool: description = %q, want default", got.Definition().Description)
	}
}
