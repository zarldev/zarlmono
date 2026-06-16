package code_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zarlai/tools/code"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestBashTool(t *testing.T) {
	root := t.TempDir()
	ws, _ := code.NewWorkspace(root)
	tool := code.NewBashTool(ws)

	t.Run("short command", func(t *testing.T) {
		res, err := tool.Execute(context.Background(), tools.ToolCall{Arguments: tools.ToolParameters{
			"command": "echo hello",
		}})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !res.Success {
			t.Fatalf("unexpected tool failure: %s", res.Error)
		}
		if !strings.Contains(service.ToolResultText(res), "hello") {
			t.Fatalf("missing output: %q", service.ToolResultText(res))
		}
		if !strings.Contains(service.ToolResultText(res), "exit") {
			t.Fatalf("missing exit info: %q", service.ToolResultText(res))
		}
	})

	t.Run("ANSI strip", func(t *testing.T) {
		res, err := tool.Execute(context.Background(), tools.ToolCall{Arguments: tools.ToolParameters{
			"command": "printf '\\033[31mred\\033[0m\\n'",
		}})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !res.Success {
			t.Fatalf("unexpected tool failure: %s", res.Error)
		}
		if strings.Contains(service.ToolResultText(res), "\x1b[") {
			t.Fatalf("ANSI not stripped: %q", service.ToolResultText(res))
		}
	})

	t.Run("timeout", func(t *testing.T) {
		res, err := tool.Execute(context.Background(), tools.ToolCall{Arguments: tools.ToolParameters{
			"command":         "sleep 5",
			"timeout_seconds": float64(1),
		}})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !strings.Contains(service.ToolResultText(res), "timed_out") && res.Success {
			t.Fatalf("expected timeout marker, got %q", service.ToolResultText(res))
		}
	})

	t.Run("cwd is workspace", func(t *testing.T) {
		res, err := tool.Execute(context.Background(), tools.ToolCall{Arguments: tools.ToolParameters{"command": "pwd"}})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !res.Success {
			t.Fatalf("unexpected tool failure: %s", res.Error)
		}
		if !strings.Contains(service.ToolResultText(res), ws.Root()) {
			t.Fatalf("pwd %q does not match root %q", service.ToolResultText(res), ws.Root())
		}
	})

	t.Run("context cancel", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, _ = tool.Execute(ctx, tools.ToolCall{Arguments: tools.ToolParameters{"command": "sleep 5"}})
		if time.Since(start) > 2*time.Second {
			t.Fatal("context cancel did not abort command")
		}
	})

	t.Run("output truncation", func(t *testing.T) {
		// Generate >1MB of output.
		res, err := tool.Execute(context.Background(), tools.ToolCall{Arguments: tools.ToolParameters{
			"command": "yes x | head -c 2000000",
		}})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !res.Success {
			t.Fatalf("unexpected tool failure: %s", res.Error)
		}
		if !strings.Contains(service.ToolResultText(res), "output_truncated") {
			t.Fatalf("expected truncation marker in %d bytes", len(service.ToolResultText(res)))
		}
	})
}
