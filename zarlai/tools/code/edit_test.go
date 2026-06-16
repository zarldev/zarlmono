package code_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/tools/code"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestEditTool(t *testing.T) {
	root := t.TempDir()
	ws, _ := code.NewWorkspace(root)
	tool := code.NewEditTool(ws)

	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	t.Run("happy path", func(t *testing.T) {
		write("a.txt", "alpha\nbeta\ngamma\n")
		res, err := tool.Execute(context.Background(), tools.ToolCall{Arguments: tools.ToolParameters{
			"path":       "a.txt",
			"old_string": "beta",
			"new_string": "BETA",
		}})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !res.Success {
			t.Fatalf("unexpected tool failure: %s", res.Error)
		}
		if read("a.txt") != "alpha\nBETA\ngamma\n" {
			t.Fatalf("not replaced: %q", read("a.txt"))
		}
	})

	t.Run("not found", func(t *testing.T) {
		write("b.txt", "hello\n")
		res, err := tool.Execute(context.Background(), tools.ToolCall{Arguments: tools.ToolParameters{
			"path":       "b.txt",
			"old_string": "world",
			"new_string": "x",
		}})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if res.Success {
			t.Fatal("expected not-found error")
		}
	})

	t.Run("ambiguous match", func(t *testing.T) {
		write("c.txt", "x\nx\n")
		res, err := tool.Execute(context.Background(), tools.ToolCall{Arguments: tools.ToolParameters{
			"path":       "c.txt",
			"old_string": "x",
			"new_string": "y",
		}})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if res.Success {
			t.Fatal("expected ambiguous-match error")
		}
	})

	t.Run("replace_all", func(t *testing.T) {
		write("d.txt", "x\nx\nx\n")
		res, err := tool.Execute(context.Background(), tools.ToolCall{Arguments: tools.ToolParameters{
			"path":        "d.txt",
			"old_string":  "x",
			"new_string":  "y",
			"replace_all": true,
		}})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !res.Success {
			t.Fatalf("unexpected tool failure: %s", res.Error)
		}
		if read("d.txt") != "y\ny\ny\n" {
			t.Fatalf("not replaced all: %q", read("d.txt"))
		}
	})
}
