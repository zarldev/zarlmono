package taskrunner_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/taskrunner"
	"github.com/zarldev/zarlmono/zarlai/taskrunner/taskrunnertest"
	"github.com/zarldev/zarlmono/zkit/agent/profile"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

// stubTool is a minimal Tool for gate-filtering tests.
type stubTool struct{ name string }

func (s stubTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: tools.ToolName(s.name)}
}
func (s stubTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	return tools.Success(call.ID, ""), nil
}

func newRegistryWithTools(names ...string) *tools.Registry {
	reg := tools.NewRegistry()
	for _, n := range names {
		reg.Register(stubTool{name: n})
	}
	return reg
}

// newTestRegistry assembles the composed registry: zkit profile
// resolution over Builtin() plus the builtin tool gates.
func newTestRegistry(reg *tools.Registry, overrides profile.OverrideStore, actionTools []tools.Tool, envModel string) taskrunner.ProfileRegistry {
	return taskrunner.NewProfileRegistry(
		profile.NewRegistry(profile.Builtin(), overrides, envModel),
		taskrunner.BuiltinToolGates(),
		nil,
		reg,
		actionTools,
	)
}

// fakeToolNamesStore is an in-memory ToolNamesOverrideStore.
type fakeToolNamesStore struct {
	rows map[profile.Name][]tools.ToolName
}

func (f *fakeToolNamesStore) ToolNames(ctx context.Context, name profile.Name) ([]tools.ToolName, error) {
	return f.rows[name], nil
}

func TestResolve_no_override_uses_profile_defaults(t *testing.T) {
	reg := newRegistryWithTools("web_search", "search_youtube", "wiki_search", "recall", "current_time", "store_memory", "notify_user", "present_findings")
	overrides := taskrunnertest.NewFakeOverrideStore()

	pr := newTestRegistry(reg, overrides, nil, "gemma4:26b")
	got, err := pr.Resolve(t.Context(), profile.NameResearcher)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Model != "gemma4:26b" {
		t.Errorf("Model = %q, want env fallback", got.Model)
	}
	if got.PromptPrefix == "" {
		t.Errorf("PromptPrefix should be the researcher default, got empty")
	}
	if got.MaxIterations != 20 {
		t.Errorf("MaxIterations = %d, want 20", got.MaxIterations)
	}
	if len(got.Tools) != 8 {
		t.Errorf("Tools len = %d, want 8", len(got.Tools))
	}
}

func TestResolve_override_replaces_model(t *testing.T) {
	reg := newRegistryWithTools("web_search", "search_youtube", "wiki_search", "recall", "current_time", "store_memory", "notify_user", "present_findings")
	overrides := taskrunnertest.NewFakeOverrideStore()
	model := "gemma4:31b"
	overrides.Rows[profile.NameResearcher] = profile.Override{Model: &model}

	pr := newTestRegistry(reg, overrides, nil, "gemma4:26b")
	got, err := pr.Resolve(t.Context(), profile.NameResearcher)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Model != "gemma4:31b" {
		t.Errorf("Model = %q, want override", got.Model)
	}
}

func TestResolve_unknown_profile_falls_back_to_default(t *testing.T) {
	reg := newRegistryWithTools("time", "recall")
	actionTools := []tools.Tool{stubTool{name: "store_memory"}}

	pr := newTestRegistry(reg, taskrunnertest.NewFakeOverrideStore(), actionTools, "gemma4:26b")
	got, err := pr.Resolve(t.Context(), "ghost")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Name != profile.NameDefault {
		t.Errorf("Name = %q, want %q", got.Name, profile.NameDefault)
	}
}

func TestResolve_empty_gate_result_is_hard_fail(t *testing.T) {
	reg := newRegistryWithTools()
	pr := newTestRegistry(reg, taskrunnertest.NewFakeOverrideStore(), nil, "gemma4:26b")
	_, err := pr.Resolve(t.Context(), profile.NameResearcher)
	if !errors.Is(err, taskrunner.ErrProfileNoTools) {
		t.Errorf("err = %v, want ErrProfileNoTools", err)
	}
}

func TestResolve_missing_tool_in_gate_is_dropped_silently(t *testing.T) {
	reg := newRegistryWithTools("web_search")
	pr := newTestRegistry(reg, taskrunnertest.NewFakeOverrideStore(), nil, "gemma4:26b")
	got, err := pr.Resolve(t.Context(), profile.NameResearcher)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got.Tools) != 1 {
		t.Errorf("Tools len = %d, want 1 (others dropped)", len(got.Tools))
	}
}

func TestResolve_max_iterations_override_clamps_to_cap(t *testing.T) {
	reg := newRegistryWithTools("web_search", "wiki_search", "recall", "time", "store_memory", "notify")
	overrides := taskrunnertest.NewFakeOverrideStore()
	bigIter := int32(999)
	overrides.Rows[profile.NameResearcher] = profile.Override{MaxIterations: &bigIter}

	pr := newTestRegistry(reg, overrides, nil, "gemma4:26b")
	got, err := pr.Resolve(t.Context(), profile.NameResearcher)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.MaxIterations != profile.DefaultMaxIterations {
		t.Errorf("MaxIterations = %d, want %d (clamped to cap)", got.MaxIterations, profile.DefaultMaxIterations)
	}
}

func TestResolve_store_error_propagates(t *testing.T) {
	reg := newRegistryWithTools("web_search", "wiki_search", "recall", "time", "store_memory", "notify")
	overrides := taskrunnertest.NewFakeOverrideStore()
	overrides.Err = errors.New("db down")

	pr := newTestRegistry(reg, overrides, nil, "gemma4:26b")
	_, err := pr.Resolve(t.Context(), profile.NameResearcher)
	if err == nil || !errors.Is(err, overrides.Err) {
		t.Errorf("err = %v, want wrap of %v", err, overrides.Err)
	}
}

func TestResolve_tool_names_override_replaces_gate(t *testing.T) {
	reg := newRegistryWithTools("web_search", "wiki_search", "recall")
	names := &fakeToolNamesStore{rows: map[profile.Name][]tools.ToolName{
		profile.NameResearcher: {"recall"},
	}}
	pr := taskrunner.NewProfileRegistry(
		profile.NewRegistry(profile.Builtin(), taskrunnertest.NewFakeOverrideStore(), "gemma4:26b"),
		taskrunner.BuiltinToolGates(),
		names,
		reg,
		nil,
	)
	got, err := pr.Resolve(t.Context(), profile.NameResearcher)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got.Tools) != 1 || got.Tools[0].Definition().Name != "recall" {
		t.Errorf("Tools = %v, want just recall (override replaces gate names)", got.Tools)
	}
}

func TestList_returns_all_builtin_profiles(t *testing.T) {
	pr := newTestRegistry(tools.NewRegistry(), taskrunnertest.NewFakeOverrideStore(), nil, "")
	got, err := pr.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("len = %d, want 3", len(got))
	}
}
