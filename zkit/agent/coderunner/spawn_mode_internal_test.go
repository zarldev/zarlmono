package coderunner

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// TestSpawnModePolicy runs the policy against the real standard tool set,
// so it verifies both the policy logic and that the file-mutating tools
// actually declare ToolSpec.Mutates (the policy reads it from the
// registry, not a hardcoded name list).
func TestSpawnModePolicy(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	reg := tools.NewRegistry()
	RegisterStandardTools(reg, ws, nil) // pm nil → bash registers foreground-only
	policy := SpawnModePolicy()

	cases := []struct {
		mode     spawn.SpawnMode
		toolName tools.ToolName
		allow    bool
	}{
		// explore: read-only — reads yes, mutation + shell no.
		{spawn.SpawnModeExplore, code.ToolNameRead, true},
		{spawn.SpawnModeExplore, code.ToolNameGrep, true},
		{spawn.SpawnModeExplore, code.ToolNameGlob, true},
		{spawn.SpawnModeExplore, code.ToolNameWrite, false},
		{spawn.SpawnModeExplore, code.ToolNameEdit, false},
		{spawn.SpawnModeExplore, code.ToolNameBash, false},

		// verify: may run tests/builds via bash, but not edit files.
		{spawn.SpawnModeVerify, code.ToolNameBash, true},
		{spawn.SpawnModeVerify, code.ToolNameRead, true},
		{spawn.SpawnModeVerify, code.ToolNameEdit, false},

		// implement and unset: full surface.
		{spawn.SpawnModeImplement, code.ToolNameEdit, true},
		{spawn.SpawnModeImplement, code.ToolNameBash, true},
		{"", code.ToolNameEdit, true},
		{"", code.ToolNameBash, true},
	}
	for _, c := range cases {
		var spec tools.ToolSpec
		if t, ok := reg.Tool(c.toolName); ok {
			spec = t.Definition()
		} else {
			spec = tools.ToolSpec{Name: c.toolName} // fallback for missing tools
		}
		if got := policy(c.mode, spec); got != c.allow {
			t.Errorf("policy(%q, %q) = %v, want %v", c.mode, c.toolName, got, c.allow)
		}
	}
}
