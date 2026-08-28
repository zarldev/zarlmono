//go:build linux

package sandbox_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/sandbox"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestVerifyModeSandboxDeniesWorkspaceWrites(t *testing.T) {
	requireLandlock(t)
	wsDir := t.TempDir()
	ws, err := code.NewWorkspace(wsDir)
	if err != nil {
		t.Fatal(err)
	}
	normalPolicy := sandbox.DefaultPolicy(ws.Root())
	normal, err := sandbox.New(normalPolicy)
	if err != nil {
		t.Fatal(err)
	}
	verify, err := sandbox.New(sandbox.VerifyPolicy(normalPolicy, ws.Root()))
	if err != nil {
		t.Fatal(err)
	}
	modeSandbox := sandbox.NewWorkModeSandbox(normal, verify)
	bash := code.NewBashTool(ws, code.WithSandbox(modeSandbox))

	run := func(ctx context.Context, command string) string {
		t.Helper()
		result, err := bash.Execute(ctx, tools.ToolCall{ID: "call", ToolName: code.ToolNameBash, Arguments: tools.ToolParameters{"command": command}})
		if err != nil {
			t.Fatal(err)
		}
		data, _ := result.Data.(string)
		return data
	}

	verifyCtx := taskscope.WithWorkMode(t.Context(), taskscope.WorkModes.VERIFY)
	for name, command := range map[string]string{
		"shell":       "touch direct-write",
		"interpreter": `python3 -c 'open("python-write", "w").write("bad")'`,
	} {
		t.Run(name, func(t *testing.T) {
			out := run(verifyCtx, command)
			if strings.Contains(out, "[exit 0]") {
				t.Fatalf("verify write succeeded: %s", out)
			}
		})
	}
	for _, path := range []string{"direct-write", "python-write"} {
		if _, err := os.Stat(filepath.Join(ws.Root(), path)); !os.IsNotExist(err) {
			t.Fatalf("verify created %s", path)
		}
	}

	out := run(taskscope.WithWorkMode(t.Context(), taskscope.WorkModes.IMPLEMENT), "touch implement-write")
	if !strings.Contains(out, "[exit 0]") {
		t.Fatalf("implement write denied: %s", out)
	}
	if _, err := os.Stat(filepath.Join(ws.Root(), "implement-write")); err != nil {
		t.Fatalf("implement file: %v", err)
	}
}

func TestVerifyPolicyKeepsWorkspaceReadableAndNotWritable(t *testing.T) {
	root := filepath.Clean(t.TempDir())
	normal := sandbox.Policy{ReadDirs: []string{"/usr"}, WriteDirs: []string{root, "/tmp"}}
	verify := sandbox.VerifyPolicy(normal, root)
	if len(verify.WriteDirs) != 0 {
		t.Fatalf("verify write dirs = %#v; grants containing the workspace must be removed", verify.WriteDirs)
	}
	if verify.ReadDirs[len(verify.ReadDirs)-1] != root {
		t.Fatalf("verify read dirs = %#v, want workspace", verify.ReadDirs)
	}
	if len(normal.WriteDirs) != 2 {
		t.Fatalf("normal policy mutated: %#v", normal.WriteDirs)
	}
}
