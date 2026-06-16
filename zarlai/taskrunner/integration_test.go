package taskrunner_test

import (
	"context"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/taskrunner"
	"github.com/zarldev/zarlmono/zarlai/taskrunner/taskrunnertest"
	"github.com/zarldev/zarlmono/zkit/agent/profile"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

// TestIntegration_profile_drives_model_and_tools exercises ProfileRegistry
// end-to-end — zkit profile resolution over Builtin() plus the builtin
// tool gates and an in-memory override store — then asserts the resolved
// profile reflects the merge.
func TestIntegration_profile_drives_model_and_tools(t *testing.T) {
	overrides := taskrunnertest.NewFakeOverrideStore()
	model := "gemma4:31b"
	overrides.Rows[profile.NameResearcher] = profile.Override{Model: &model}

	reg := taskrunner.NewProfileRegistry(
		profile.NewRegistry(profile.Builtin(), overrides, "gemma4:26b"),
		taskrunner.BuiltinToolGates(),
		nil,
		registryWithResearcherTools(t),
		nil,
	)

	got, err := reg.Resolve(t.Context(), profile.NameResearcher)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Model != "gemma4:31b" {
		t.Errorf("override should pin model, got %q", got.Model)
	}
	if got.Name != profile.NameResearcher {
		t.Errorf("Name = %q, want researcher", got.Name)
	}
	if len(got.Tools) != 8 {
		t.Errorf("Tools len = %d, want 8", len(got.Tools))
	}

	// Unknown profile falls back to default.
	fallback, err := reg.Resolve(t.Context(), "nonsense")
	if err != nil {
		t.Fatalf("Resolve fallback: %v", err)
	}
	if fallback.Name != profile.NameDefault {
		t.Errorf("fallback Name = %q, want default", fallback.Name)
	}
}

func registryWithResearcherTools(t *testing.T) *tools.Registry {
	t.Helper()
	reg := tools.NewRegistry()
	for _, name := range []string{"web_search", "search_youtube", "wiki_search", "recall", "current_time", "store_memory", "notify_user", "present_findings"} {
		reg.Register(integrationStubTool{name: name})
	}
	return reg
}

// integrationStubTool is a minimal Tool for the integration test.
// Named distinctly from stubTool (defined in profile_registry_test.go) to avoid collisions.
type integrationStubTool struct{ name string }

func (s integrationStubTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: tools.ToolName(s.name)}
}
func (s integrationStubTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	return tools.Success(call.ID, ""), nil
}
