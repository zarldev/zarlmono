package coderunner_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestSpawnModePolicy(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	reg := tools.NewRegistry()
	coderunner.RegisterStandardTools(reg, ws, nil)
	policy := coderunner.SpawnModePolicy()

	cases := []struct {
		mode     spawn.SpawnMode
		toolName tools.ToolName
		allow    bool
	}{
		{spawn.SpawnModeExplore, code.ToolNameRead, true},
		{spawn.SpawnModeExplore, code.ToolNameGrep, true},
		{spawn.SpawnModeExplore, code.ToolNameGlob, true},
		{spawn.SpawnModeExplore, code.ToolNameWrite, false},
		{spawn.SpawnModeExplore, code.ToolNameEdit, false},
		{spawn.SpawnModeExplore, code.ToolNameBash, false},
		{spawn.SpawnModeVerify, code.ToolNameBash, true},
		{spawn.SpawnModeVerify, code.ToolNameRead, true},
		{spawn.SpawnModeVerify, code.ToolNameEdit, false},
		{spawn.SpawnModeImplement, code.ToolNameEdit, true},
		{spawn.SpawnModeImplement, code.ToolNameBash, true},
		{"", code.ToolNameEdit, true},
		{"", code.ToolNameBash, true},
	}
	for _, tc := range cases {
		registered, ok := reg.Tool(tc.toolName)
		if !ok {
			t.Fatalf("standard tool %q not registered", tc.toolName)
		}
		if got := policy(tc.mode, registered.Definition()); got != tc.allow {
			t.Errorf("policy(%q, %q) = %v, want %v", tc.mode, tc.toolName, got, tc.allow)
		}
	}
}
