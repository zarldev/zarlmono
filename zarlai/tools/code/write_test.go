package code_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/tools/code"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestWriteTool(t *testing.T) {
	root := t.TempDir()
	ws, _ := code.NewWorkspace(root)
	tool := code.NewWriteTool(ws)

	t.Run("happy path", func(t *testing.T) {
		res, err := tool.Execute(context.Background(), tools.ToolCall{Arguments: tools.ToolParameters{
			"path":    "out.txt",
			"content": "hello world\n",
		}})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !res.Success {
			t.Fatalf("unexpected tool failure: %s", res.Error)
		}
		got, err := os.ReadFile(filepath.Join(root, "out.txt"))
		if err != nil || string(got) != "hello world\n" {
			t.Fatalf("file not written correctly: %q err=%v", got, err)
		}
	})

	t.Run("creates parent dirs", func(t *testing.T) {
		res, err := tool.Execute(context.Background(), tools.ToolCall{Arguments: tools.ToolParameters{
			"path":    "deep/nested/dir/file.txt",
			"content": "x",
		}})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !res.Success {
			t.Fatalf("unexpected tool failure: %s", res.Error)
		}
		if _, err := os.Stat(filepath.Join(root, "deep/nested/dir/file.txt")); err != nil {
			t.Fatalf("expected file: %v", err)
		}
	})

	t.Run("escape refusal", func(t *testing.T) {
		res, err := tool.Execute(context.Background(), tools.ToolCall{Arguments: tools.ToolParameters{
			"path":    "../outside.txt",
			"content": "x",
		}})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if res.Success {
			t.Fatal("expected escape error")
		}
	})
}
