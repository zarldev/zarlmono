package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/cli"
)

func TestResolvePromptSources(t *testing.T) {
	t.Run("inline", func(t *testing.T) {
		got, err := cli.ResolvePrompt("", "  keep spacing  ")
		if err != nil || got != "  keep spacing  " {
			t.Fatalf("ResolvePrompt() = %q, %v", got, err)
		}
	})

	t.Run("file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "prompt.txt")
		if err := os.WriteFile(path, []byte("from file\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := cli.ResolvePrompt(path, "")
		if err != nil || got != "from file\n" {
			t.Fatalf("ResolvePrompt() = %q, %v", got, err)
		}
	})

	t.Run("empty", func(t *testing.T) {
		got, err := cli.ResolvePrompt("", " \t ")
		if err != nil || got != "" {
			t.Fatalf("ResolvePrompt() = %q, %v", got, err)
		}
	})

	t.Run("empty file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "empty.txt")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := cli.ResolvePrompt(path, "")
		if err != nil || got != "" {
			t.Fatalf("ResolvePrompt() = %q, %v", got, err)
		}
	})
}

func TestResolvePromptRejectsMultipleSources(t *testing.T) {
	_, err := cli.ResolvePrompt("prompt.txt", "inline")
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("ResolvePrompt() error = %v", err)
	}
}

func TestResolvePromptReportsReadErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.txt")
	_, err := cli.ResolvePrompt(path, "")
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("ResolvePrompt() error = %v", err)
	}
}
