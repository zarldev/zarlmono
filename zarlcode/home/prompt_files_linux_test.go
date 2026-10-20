//go:build linux

package home_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/home"
)

func TestPromptSpecialFilesDoNotBlock(t *testing.T) {
	if dir := os.Getenv("ZARLCODE_PROMPT_FIFO_DIR"); dir != "" {
		resolved := home.ResolveBuildPromptDir(dir, "embedded")
		if resolved.Body != "embedded" || resolved.Preferences != "" || len(resolved.Diagnostics) != 2 {
			t.Fatalf("special prompt files: %+v", resolved)
		}
		return
	}
	dir := t.TempDir()
	for _, name := range []string{home.PreferencesFile, home.PromptOverrideFile} {
		if err := syscall.Mkfifo(filepath.Join(dir, name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestPromptSpecialFilesDoNotBlock$")
	cmd.Env = append(os.Environ(), "ZARLCODE_PROMPT_FIFO_DIR="+dir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("prompt file resolution: %v (context: %v)\n%s", err, ctx.Err(), output)
	}
}

func TestPromptFileBoundsAndRegularSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "preferences.md")
	if err := os.WriteFile(target, []byte("Keep it concise."), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, home.PreferencesFile)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, home.PromptOverrideFile), []byte(strings.Repeat("x", home.MaxPromptFileBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved := home.ResolveBuildPromptDir(dir, "embedded")
	if resolved.Body != "embedded" || resolved.Preferences != "Keep it concise." || !resolved.UsePreferences || len(resolved.Diagnostics) != 1 {
		t.Fatalf("bounded prompt resolution: mode=%s preferences=%q diagnostics=%v", resolved.Mode, resolved.Preferences, resolved.Diagnostics)
	}
}
