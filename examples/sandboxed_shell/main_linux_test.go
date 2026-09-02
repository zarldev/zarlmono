//go:build linux

package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSandboxedShell(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "sandboxed-shell")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, ".")
	build.Dir = "."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build example: %v\n%s", err, output)
	}
	cmd := exec.CommandContext(t.Context(), binary)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run example: %v\n%s", err, output)
	}
	got := string(output)
	if strings.Contains(got, "skipped") {
		return
	}
	if !strings.Contains(got, "workspace write: confined") || !strings.Contains(got, "outside write: denied") {
		t.Fatalf("output = %q", got)
	}
	if _, err := os.Stat(binary); err != nil {
		t.Fatal(err)
	}
}
