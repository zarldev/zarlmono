package dynamic_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"
)

func TestNewToolRendersCanonicalSource(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     tools.ToolParameters
		contains []string
	}{
		{
			name: "typed args and imports",
			args: tools.ToolParameters{
				"name": "shout", "description": "Uppercase the input.",
				"args_fields": "Text string `json:\"text\" doc:\"the input string\"`",
				"out_type":    "string", "body": "return strings.ToUpper(args.Text), nil",
				"imports": "\"strings\"\n\n\"time\"\n",
			},
			contains: []string{
				"package main", `"strings"`, `"time"`,
				`"github.com/zarldev/zarlmono/zkit/ai/tools/toolkit"`,
				"type Args struct {", "Text string `json:\"text\" doc:\"the input string\"`",
				"toolkit.Run(toolkit.Tool[Args, string]{", `Name:        "shout"`,
				"Description: `Uppercase the input.`", "return strings.ToUpper(args.Text), nil",
			},
		},
		{
			name: "no args uses default output type",
			args: tools.ToolParameters{
				"name": "ping", "description": "Returns pong.", "body": `return "pong", nil`,
			},
			contains: []string{"type Args struct {", "toolkit.Run(toolkit.Tool[Args, string]{", `return "pong", nil`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ws := t.TempDir()
			registrar := dynamic.NewRegistrar(dynamic.NewCatalog(dynamic.NewFileStore(filepath.Join(t.TempDir(), "catalog.json"))), tools.NewRegistry())
			tool := dynamic.NewNewToolTool(dynamic.NewBuildTool(registrar, ws), ws)
			result, err := tool.Execute(t.Context(), tools.ToolCall{ID: "new", Arguments: tt.args})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			// The fixture intentionally has no workspace go.mod: execution still
			// exposes the public scaffolding behavior before build validation.
			if result.Success || !strings.Contains(result.Error, "no go.mod") {
				t.Fatalf("result = %+v, want post-render build validation error", result)
			}
			path := filepath.Join(ws, "tools", tt.args["name"].(string), "main.go")
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read rendered source: %v", err)
			}
			for _, want := range tt.contains {
				if !strings.Contains(string(src), want) {
					t.Errorf("rendered source missing %q\n--- got ---\n%s", want, src)
				}
			}
			if strings.Contains(string(src), "go.mod") {
				t.Error("rendered source must not mention go.mod")
			}
		})
	}
}
