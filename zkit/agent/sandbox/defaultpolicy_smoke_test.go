//go:build linux

package sandbox_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/sandbox"
)

// TestDefaultPolicyToolchainSmoke proves the default grants don't
// cripple a real coding workflow: git commits (object writes + renames
// under .git) and go builds (GOCACHE under ~/.cache, toolchain under
// /usr) both succeed inside the sandbox. If this fails after a policy
// change, the agent's bash tool just broke for every consumer.
func TestDefaultPolicyToolchainSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("toolchain smoke is slow")
	}
	requireLandlock(t)
	for _, bin := range []string{"git", "go"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}

	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "go.mod"), []byte("module smoke\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	script := `set -e
git init -q .
git add .
git -c user.email=smoke@test -c user.name=smoke commit -qm smoke
go build -o smoke-bin .
./smoke-bin
git log --oneline | head -1`
	out, code := runSandboxed(t, sandbox.DefaultPolicy(ws), ws, script)
	if code != 0 {
		t.Fatalf("toolchain workflow broke under default policy (exit %d):\n%s", code, out)
	}
}
