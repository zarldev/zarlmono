package code_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zarlai/tools/code"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

func requireRipgrep(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep not installed; skipping")
	}
}

func TestGrepTool(t *testing.T) {
	requireRipgrep(t)
	root := t.TempDir()
	ws, _ := code.NewWorkspace(root)
	tool := code.NewGrepTool(ws)

	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package x\nfunc Foo() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("foo bar\nbaz Foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("happy path", func(t *testing.T) {
		res, err := tool.Execute(context.Background(), tools.ToolCall{Arguments: tools.ToolParameters{"pattern": "Foo"}})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !res.Success {
			t.Fatalf("unexpected tool failure: %s", res.Error)
		}
		var hits []map[string]any
		if err := json.Unmarshal([]byte(service.ToolResultText(res)), &hits); err != nil {
			t.Fatalf("non-JSON output: %v\n%s", err, service.ToolResultText(res))
		}
		if len(hits) < 2 {
			t.Fatalf("expected ≥2 hits, got %d", len(hits))
		}
	})

	t.Run("glob filter", func(t *testing.T) {
		res, err := tool.Execute(context.Background(), tools.ToolCall{Arguments: tools.ToolParameters{"pattern": "Foo", "glob": "*.go"}})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !res.Success {
			t.Fatalf("unexpected tool failure: %s", res.Error)
		}
		if strings.Contains(service.ToolResultText(res), "b.txt") {
			t.Fatalf("glob did not filter: %s", service.ToolResultText(res))
		}
	})
}

func TestGrepTool_NoRipgrep(t *testing.T) {
	if _, err := exec.LookPath("rg"); err == nil {
		t.Skip("ripgrep is installed; cannot test missing-rg path")
	}
	ws, _ := code.NewWorkspace(t.TempDir())
	tool := code.NewGrepTool(ws)
	res, err := tool.Execute(context.Background(), tools.ToolCall{Arguments: tools.ToolParameters{"pattern": "x"}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Success {
		t.Fatal("expected error when rg missing")
	}
}
