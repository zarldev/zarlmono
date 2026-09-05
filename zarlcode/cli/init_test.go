package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/cli"
)

func TestRunInitCreatesAndPreservesHome(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	var first bytes.Buffer
	if code := cli.RunInit(&first); code != 0 {
		t.Fatalf("RunInit() exit = %d, output = %q", code, first.String())
	}
	root := filepath.Join(homeDir, ".zarlcode")
	for _, name := range []string{"skills", "tools", "hooks"} {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil || !info.IsDir() {
			t.Fatalf("%s directory: info=%v err=%v", name, info, err)
		}
	}
	if !strings.Contains(first.String(), "zarlcode home at "+root) || !strings.Contains(first.String(), "created: skills/, tools/, hooks/") {
		t.Fatalf("first output = %q", first.String())
	}

	marker := filepath.Join(root, "skills", "mine.md")
	want := []byte("preserve exactly\n")
	if err := os.WriteFile(marker, want, 0o600); err != nil {
		t.Fatal(err)
	}
	var second bytes.Buffer
	if code := cli.RunInit(&second); code != 0 {
		t.Fatalf("second RunInit() exit = %d, output = %q", code, second.String())
	}
	got, err := os.ReadFile(marker)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("preserved file = %q, %v", got, err)
	}
	if strings.Contains(second.String(), "created:") || !strings.Contains(second.String(), "existed: skills/, tools/, hooks/") {
		t.Fatalf("second output = %q", second.String())
	}
}

func TestRunInitReturnsFailureWithoutWritingOutput(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	if err := os.WriteFile(filepath.Join(homeDir, ".zarlcode"), []byte("conflict"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := cli.RunInit(&out); code != 1 {
		t.Fatalf("RunInit() exit = %d, want 1", code)
	}
	if out.Len() != 0 {
		t.Fatalf("RunInit() output = %q, want empty", out.String())
	}
}
