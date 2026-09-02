package main_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalMCPExample(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "local-mcp")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build example: %v\n%s", err, output)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run example: %v\n%s", err, output)
	}
	if ctx.Err() != nil {
		t.Fatalf("example did not shut down cleanly: %v", ctx.Err())
	}

	got := strings.TrimSpace(string(output))
	want := strings.Join([]string{
		"connected: initialization complete",
		"discovered: echo",
		"result: echo: hello, MCP",
		"disconnected: server stopped",
	}, "\n")
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}
