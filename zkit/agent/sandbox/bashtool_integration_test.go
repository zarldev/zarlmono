//go:build linux

package sandbox_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/sandbox"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// TestBashToolSandboxed drives the real tool stack: BashTool wraps the
// command, Sandbox rewrites it through the shim (TestMain), Landlock
// enforces, and the tool annotates the denial. This is the integration
// the consumers (zarlcode TUI, swebench driver) rely on.
func TestBashToolSandboxed(t *testing.T) {
	requireLandlock(t)
	wsDir := t.TempDir()
	outside := t.TempDir()
	ws, err := code.NewWorkspace(wsDir)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := sandbox.New(narrowPolicy(ws.Root()))
	if err != nil {
		t.Fatal(err)
	}
	bash := code.NewBashTool(ws, code.WithSandbox(sb))

	run := func(t *testing.T, command string) string {
		t.Helper()
		args := tools.ToolParameters{"command": command}
		res, err := bash.Execute(t.Context(), tools.ToolCall{ID: "t1", ToolName: code.ToolNameBash, Arguments: args})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		out, ok := res.Data.(string)
		if !ok {
			t.Fatalf("result data is %T, want string", res.Data)
		}
		return out
	}

	t.Run("workspace command succeeds", func(t *testing.T) {
		out := run(t, "echo from-sandbox && pwd")
		if !strings.Contains(out, "from-sandbox") || !strings.Contains(out, "[exit 0]") {
			t.Fatalf("unexpected output: %s", out)
		}
	})

	t.Run("escape denied and annotated", func(t *testing.T) {
		out := run(t, "touch "+filepath.Join(outside, "escape"))
		if strings.Contains(out, "[exit 0]") {
			t.Fatalf("write outside workspace succeeded: %s", out)
		}
		if !strings.Contains(out, "sandbox:") {
			t.Fatalf("denial missing sandbox annotation: %s", out)
		}
		if _, err := os.Stat(filepath.Join(outside, "escape")); !os.IsNotExist(err) {
			t.Fatalf("escape file exists — sandbox did not hold")
		}
	})
}

// TestBackgroundProcessSandboxed proves the ProcessManager path runs
// under the same confinement as foreground bash.
func TestBackgroundProcessSandboxed(t *testing.T) {
	requireLandlock(t)
	wsDir := t.TempDir()
	outside := t.TempDir()
	ws, err := code.NewWorkspace(wsDir)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := sandbox.New(narrowPolicy(ws.Root()))
	if err != nil {
		t.Fatal(err)
	}
	pm := code.NewProcessManager(ws, code.WithProcessSandbox(sb))
	defer pm.Close(t.Context())

	id, err := pm.StartProcess("touch " + filepath.Join(outside, "bg-escape") + "; touch inside-ok")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitExit(t, pm, id)
	if _, err := os.Stat(filepath.Join(outside, "bg-escape")); !os.IsNotExist(err) {
		t.Fatal("background process escaped the sandbox")
	}
	if _, err := os.Stat(filepath.Join(ws.Root(), "inside-ok")); err != nil {
		t.Fatalf("background process couldn't write inside workspace: %v", err)
	}
}

func waitExit(t *testing.T, pm *code.ProcessManager, id string) {
	t.Helper()
	deadline := t.Context().Done()
	for range 200 {
		info, err := pm.Info(id)
		if err != nil {
			t.Fatalf("process %s vanished: %v", id, err)
		}
		if !info.Running {
			return
		}
		select {
		case <-deadline:
			t.Fatal("context done waiting for process exit")
		case <-time.After(25 * time.Millisecond):
		}
	}
	t.Fatal("process did not exit in time")
}
