package taskrunner_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zarlai/taskrunner"
	"github.com/zarldev/zarlmono/zkit/agent/profile"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestBuiltinToolGates_covers_every_builtin_profile(t *testing.T) {
	gates := taskrunner.BuiltinToolGates()
	for _, p := range profile.Builtin() {
		if _, ok := gates[p.Name]; !ok {
			t.Errorf("BuiltinToolGates missing gate for builtin profile %q", p.Name)
		}
	}
}

func TestBuiltinToolGates_default_is_dynamic(t *testing.T) {
	gate := taskrunner.BuiltinToolGates()[profile.NameDefault]
	if gate.Tools != nil || gate.Providers != nil {
		t.Errorf("default gate should be empty (dynamic), got %+v", gate)
	}
}

func TestBuiltinToolGates_researcher_gate_is_read_only(t *testing.T) {
	gate := taskrunner.BuiltinToolGates()[profile.NameResearcher]
	expected := map[tools.ToolName]bool{
		"web_search": true, "search_youtube": true, "wiki_search": true, "recall": true,
		"current_time": true, "store_memory": true, "notify_user": true, "present_findings": true,
	}
	if len(gate.Tools) != len(expected) {
		t.Fatalf("researcher gate len = %d, want %d (%v)", len(gate.Tools), len(expected), gate.Tools)
	}
	for _, name := range gate.Tools {
		if !expected[name] {
			t.Errorf("researcher gate contains unexpected tool %q", name)
		}
	}
	if len(gate.Providers) != 1 || gate.Providers[0] != "obsidian" {
		t.Errorf("researcher gate providers = %v, want [obsidian]", gate.Providers)
	}
}

func TestBuiltinToolGates_coder_gate_includes_code_tools(t *testing.T) {
	gate := taskrunner.BuiltinToolGates()[profile.NameCoder]
	expected := map[tools.ToolName]bool{
		"read":             true,
		"write":            true,
		"edit":             true,
		"grep":             true,
		"ls":               true,
		"bash":             true,
		"current_time":     true,
		"present_findings": true,
	}
	if len(gate.Tools) != len(expected) {
		t.Fatalf("coder gate len = %d, want %d (%v)", len(gate.Tools), len(expected), gate.Tools)
	}
	for _, name := range gate.Tools {
		if !expected[name] {
			t.Errorf("coder gate contains unexpected tool %q", name)
		}
	}
}
