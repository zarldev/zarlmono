//go:build linux

package instructions_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/instructions"
)

func TestInstructionSpecialFilesDoNotBlock(t *testing.T) {
	if root := os.Getenv("ZARLCODE_INSTRUCTION_FIFO_ROOT"); root != "" {
		for name, discover := range map[string]func(string, int) ([]instructions.Document, []error){
			"root":      instructions.DiscoverRoot,
			"recursive": instructions.Discover,
		} {
			docs, errs := discover(root, instructions.DefaultMaxBytes)
			if len(docs) != 1 || docs[0].Name != "CLAUDE.md" || len(errs) != 1 || !strings.Contains(errs[0].Error(), "AGENTS.md") {
				t.Fatalf("%s: documents=%v errors=%v", name, docs, errs)
			}
		}
		if _, err := instructions.LoadOne(root, "AGENTS.md"); err == nil {
			t.Fatal("LoadOne accepted a FIFO")
		}
		return
	}
	for _, kind := range []string{"fifo", "symlink_to_fifo"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "AGENTS.md")
			fifo := path
			if kind == "symlink_to_fifo" {
				fifo = filepath.Join(root, "pipe")
			}
			if err := syscall.Mkfifo(fifo, 0o600); err != nil {
				t.Fatal(err)
			}
			if path != fifo {
				if err := os.Symlink(fifo, path); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("Valid instructions."), 0o600); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			// The subprocess owns the potentially blocking read. Its deadline
			// kills and joins it even when testing the unfixed implementation.
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestInstructionSpecialFilesDoNotBlock$")
			cmd.Env = append(os.Environ(), "ZARLCODE_INSTRUCTION_FIFO_ROOT="+root)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("special-file discovery: %v (context: %v)\n%s", err, ctx.Err(), output)
			}
		})
	}
}

func TestInstructionReadBoundsAndRegularSymlinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "rules.md")
	if err := os.WriteFile(target, []byte("Shared rules."), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(strings.Repeat("x", instructions.MaxFileBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, discover := range []func(string, int) ([]instructions.Document, []error){instructions.DiscoverRoot, instructions.Discover} {
		docs, errs := discover(root, 0)
		if len(docs) != 1 || docs[0].Content != "Shared rules." || len(errs) != 1 {
			t.Fatalf("documents=%v errors=%v", docs, errs)
		}
	}
	if _, err := instructions.LoadOne(root, "AGENTS.md"); err == nil {
		t.Fatal("LoadOne accepted oversized instructions")
	}
	doc, err := instructions.LoadOne(root, "CLAUDE.md")
	if err != nil || doc.Content != "Shared rules." {
		t.Fatalf("LoadOne regular symlink = %+v, %v", doc, err)
	}
}
