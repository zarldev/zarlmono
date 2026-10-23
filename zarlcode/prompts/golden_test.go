package prompts_test

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/prompts"
)

var updateGolden = flag.Bool("update", false, "rewrite prompt golden files")

func TestRenderGoldenPrompts(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		data     prompts.Data
		maxBytes int
	}{
		{
			name:     "build_compact",
			maxBytes: 3564,
			body:     prompts.SystemCompact,
			data:     prompts.Data{WorkspaceRoot: "/repo"},
		},
		{
			name:     "build_lean",
			maxBytes: 5486,
			body:     prompts.System,
			data:     prompts.Data{WorkspaceRoot: "/repo"},
		},
		{
			name:     "build_full",
			maxBytes: 7724,
			body:     prompts.System,
			data: prompts.Data{
				WorkspaceRoot:     "/repo",
				Tools:             []prompts.ToolInfo{{Name: "read", Description: "read files"}, {Name: "new_tool", Description: "author a tool"}, {Name: "register_tool", Description: "register existing tool"}, {Name: "update_plan", Description: "update structured plan"}, {Name: "program", Description: "read/search fan-out"}},
				SelfMod:           true,
				CanAuthorTool:     true,
				CanRegisterTool:   true,
				Planning:          true,
				ProgrammaticTools: true,
				UserPreferences:   "Prefer terse updates.",
				InstructionDocs:   []prompts.InstructionDoc{{Path: "AGENTS.md", Content: "Run focused tests."}},
			},
		},
		{
			name:     "plan",
			maxBytes: 9688,
			body:     prompts.Plan,
			data: prompts.Data{
				WorkspaceRoot:     "/repo",
				Tools:             []prompts.ToolInfo{{Name: "read", Description: "read files"}, {Name: "update_plan", Description: "update structured plan"}, {Name: "program", Description: "read/search fan-out"}},
				Planning:          true,
				ProgrammaticTools: true,
				UserPreferences:   "Prefer plans with risks called out.",
				InstructionDocs:   []prompts.InstructionDoc{{Path: "AGENTS.md", Content: "Keep package-local guidance local."}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := prompts.Render(tt.name, tt.body, tt.data)
			if err != nil {
				t.Fatal(err)
			}
			// Keep rendered-byte budgets separate from token/cost claims and from
			// golden regeneration, which must not silently relax the size gate.
			if len(got) > tt.maxBytes {
				t.Errorf("rendered bytes = %d, want <= %d", len(got), tt.maxBytes)
			}
			t.Logf("rendered prompt: %d bytes", len(got))
			path := filepath.Join("testdata", tt.name+".golden.md")
			if *updateGolden {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Fatalf("rendered prompt differs from %s; run go test -C zarlcode ./prompts -run TestRenderGoldenPrompts -update", path)
			}
		})
	}
}
