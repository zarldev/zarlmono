package code_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zarlai/tools/code"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestReadTool(t *testing.T) {
	root := t.TempDir()
	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	tool := code.NewReadTool(ws)

	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "binary.bin"), []byte{0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("happy path", func(t *testing.T) {
		res, err := tool.Execute(context.Background(), tools.ToolCall{Arguments: tools.ToolParameters{"path": "hello.txt"}})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !res.Success {
			t.Fatalf("unexpected tool failure: %s", res.Error)
		}
		if !strings.Contains(service.ToolResultText(res), "1\tone") {
			t.Fatalf("expected line-numbered output, got %q", service.ToolResultText(res))
		}
	})

	t.Run("offset and limit", func(t *testing.T) {
		res, err := tool.Execute(context.Background(), tools.ToolCall{Arguments: tools.ToolParameters{
			"path":   "hello.txt",
			"offset": float64(1),
			"limit":  float64(1),
		}})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !res.Success {
			t.Fatalf("unexpected tool failure: %s", res.Error)
		}
		if !strings.Contains(service.ToolResultText(res), "two") || strings.Contains(service.ToolResultText(res), "one") {
			t.Fatalf("expected just 'two', got %q", service.ToolResultText(res))
		}
	})

	t.Run("missing file", func(t *testing.T) {
		res, err := tool.Execute(context.Background(), tools.ToolCall{Arguments: tools.ToolParameters{"path": "nope.txt"}})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if res.Success {
			t.Fatal("expected error for missing file")
		}
	})

	t.Run("binary refusal", func(t *testing.T) {
		res, err := tool.Execute(context.Background(), tools.ToolCall{Arguments: tools.ToolParameters{"path": "binary.bin"}})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if res.Success {
			t.Fatal("expected error for binary file")
		}
	})

	t.Run("escape refusal", func(t *testing.T) {
		res, err := tool.Execute(context.Background(), tools.ToolCall{Arguments: tools.ToolParameters{"path": "../escape"}})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if res.Success {
			t.Fatal("expected error for path escape")
		}
	})
}
