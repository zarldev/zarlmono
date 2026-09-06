package tuismoke_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/tools/tuismoke"
)

func TestMain(m *testing.M) {
	if os.Getenv("ZARLMONO_SMOKE_TEST_HELPER") == "1" {
		// A deliberately stalled app: it accepts input but never leaves setup.
		// tmux owns it; killing the isolated server closes its terminal.
		fmt.Fprintln(os.Stdout, "first-run setup")
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}
	os.Exit(m.Run())
}

func TestCancellationRemovesIsolatedState(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	t.Setenv("TMPDIR", parent)
	t.Setenv("ZARLMONO_SMOKE_TEST_HELPER", "1")
	// Cancellation must end the blocked transition before its own 10s timeout.
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	err = tuismoke.Run(ctx, t.TempDir(), binary, 10*time.Second, io.Discard)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled walkthrough = %v; want deadline exceeded", err)
	}
	leftovers, err := filepath.Glob(filepath.Join(parent, "zarlcode-tui-smoke-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("cancelled walkthrough leaked state/socket: %v", leftovers)
	}
}
