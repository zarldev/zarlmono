package code_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zarlai/tools/code"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestLsTool(t *testing.T) {
	root := t.TempDir()
	ws, _ := code.NewWorkspace(root)
	tool := code.NewLsTool(ws)

	os.WriteFile(filepath.Join(root, "a.txt"), []byte("hi"), 0o644)
	os.WriteFile(filepath.Join(root, ".hidden"), []byte("h"), 0o644)
	os.MkdirAll(filepath.Join(root, "sub"), 0o755)

	t.Run("default hides dotfiles", func(t *testing.T) {
		res, err := tool.Execute(context.Background(), tools.ToolCall{Arguments: tools.ToolParameters{"path": "."}})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !res.Success {
			t.Fatalf("unexpected tool failure: %s", res.Error)
		}
		var entries []map[string]any
		if err := json.Unmarshal([]byte(service.ToolResultText(res)), &entries); err != nil {
			t.Fatalf("non-JSON: %v: %s", err, service.ToolResultText(res))
		}
		for _, e := range entries {
			if e["name"] == ".hidden" {
				t.Fatalf("hidden file leaked: %v", entries)
			}
		}
	})

	t.Run("show_hidden", func(t *testing.T) {
		res, err := tool.Execute(context.Background(), tools.ToolCall{Arguments: tools.ToolParameters{"path": ".", "show_hidden": true}})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !res.Success {
			t.Fatalf("unexpected tool failure: %s", res.Error)
		}
		if !contains(service.ToolResultText(res), ".hidden") {
			t.Fatalf("hidden file missing: %s", service.ToolResultText(res))
		}
	})

	t.Run("escape refusal", func(t *testing.T) {
		res, err := tool.Execute(context.Background(), tools.ToolCall{Arguments: tools.ToolParameters{"path": "../"}})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if res.Success {
			t.Fatal("expected escape error")
		}
	})
}

func contains(s, sub string) bool { return len(s) > 0 && len(sub) > 0 && (indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
