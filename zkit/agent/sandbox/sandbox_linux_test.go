//go:build linux

package sandbox_test

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/sandbox"
)

// TestMain installs the shim hook exactly like a wired binary does:
// when a test below re-execs the test binary as a sandbox child,
// ExecShim applies the policy and execs the real command instead of
// running the test suite again.
func TestMain(m *testing.M) {
	sandbox.ExecShim()
	os.Exit(m.Run())
}

// narrowPolicy grants just enough to run /bin/bash plus read/write in
// ws — and deliberately NOT /tmp wholesale, so sibling temp dirs act
// as "outside" targets even though everything lives under /tmp.
func narrowPolicy(ws string) sandbox.Policy {
	return sandbox.Policy{
		AllowNetwork: true,
		ReadDirs:     []string{"/usr", "/bin", "/sbin", "/lib", "/lib32", "/lib64", "/etc", "/proc", "/dev"},
		WriteDirs:    []string{ws},
		WriteFiles:   []string{"/dev/null"},
	}
}

// runSandboxed executes script under bash confined by p, rooted at ws.
func runSandboxed(t *testing.T, p sandbox.Policy, ws, script string) (string, int) {
	t.Helper()
	sb, err := sandbox.New(p)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cmd := exec.Command("/bin/bash", "-c", script)
	cmd.Dir = ws
	if err := sb.Sandbox(cmd); err != nil {
		t.Fatalf("Sandbox: %v", err)
	}
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("run %q: %v\noutput: %s", script, err, out)
		}
		code = ee.ExitCode()
	}
	return string(out), code
}

func requireLandlock(t *testing.T) {
	t.Helper()
	if sandbox.ABIVersion() < 1 {
		t.Skip("landlock unavailable on this kernel")
	}
}

func TestSandboxedCommands(t *testing.T) {
	requireLandlock(t)
	ws := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("hidden"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		script   string
		wantOK   bool
		wantOut  string
		skipWhen string
	}{
		{
			name:    "echo runs and emits output",
			script:  "echo sandboxed-ok",
			wantOK:  true,
			wantOut: "sandboxed-ok",
		},
		{
			name:   "writes inside workspace allowed",
			script: "printf data > inside.txt && cat inside.txt",
			wantOK: true, wantOut: "data",
		},
		{
			name:   "cross-directory rename inside workspace allowed",
			script: "mkdir -p a b && touch a/f && mv a/f b/f",
			wantOK: true,
		},
		{
			name:   "exec from writable dir allowed",
			script: `cp /usr/bin/env ./t-env && ./t-env true`,
			wantOK: true,
		},
		{
			name:   "write outside workspace denied",
			script: fmt.Sprintf("touch %q/escape.txt", outside),
			wantOK: false, wantOut: "Permission denied",
		},
		{
			name:   "read outside grants denied",
			script: fmt.Sprintf("cat %q", secret),
			wantOK: false, wantOut: "Permission denied",
		},
		{
			name:   "ungranted tree is invisible",
			script: fmt.Sprintf("ls %q", outside),
			wantOK: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, code := runSandboxed(t, narrowPolicy(ws), ws, tc.script)
			if ok := code == 0; ok != tc.wantOK {
				t.Fatalf("exit %d, want success=%v\noutput: %s", code, tc.wantOK, out)
			}
			if tc.wantOut != "" && !strings.Contains(out, tc.wantOut) {
				t.Fatalf("output %q missing %q", out, tc.wantOut)
			}
		})
	}
}

func TestNetworkDenied(t *testing.T) {
	requireLandlock(t)
	ws := t.TempDir()

	// A live listener on host loopback: reachable proves shared netns,
	// unreachable proves isolation (not just a closed port).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	dial := fmt.Sprintf("exec 3<>/dev/tcp/127.0.0.1/%d", port)

	allow := narrowPolicy(ws)
	out, code := runSandboxed(t, allow, ws, dial)
	if code != 0 {
		t.Fatalf("network-allowed dial should reach host loopback, exit %d: %s", code, out)
	}

	deny := narrowPolicy(ws)
	deny.AllowNetwork = false
	sb, err := sandbox.New(deny)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/bash", "-c", dial)
	cmd.Dir = ws
	if err := sb.Sandbox(cmd); err != nil {
		t.Fatal(err)
	}
	out2, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("network-denied dial reached the host listener: %s", out2)
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		// Likely user namespaces unavailable — clone failed, not the dial.
		t.Skipf("netns child did not start (userns unavailable?): %v", err)
	}

	// Inside the denied namespace, the command's OWN loopback works —
	// the shim brought lo up — it just isn't the host's.
	selfLo := "exec 3<>/dev/tcp/127.0.0.1/1 && echo open || echo refused"
	out3, code := runSandboxed(t, deny, ws, selfLo)
	if code != 0 || !strings.Contains(out3, "refused") {
		t.Fatalf("own-loopback probe: exit %d output %q (want refused — lo up but nothing listening)", code, out3)
	}
}

func TestDefaultPolicy(t *testing.T) {
	ws := t.TempDir()
	p := sandbox.DefaultPolicy(ws)
	if !p.AllowNetwork {
		t.Error("default policy should allow network")
	}
	if !contains(p.WriteDirs, ws) {
		t.Errorf("workspace %q missing from write grants %v", ws, p.WriteDirs)
	}
	if !contains(p.WriteDirs, "/tmp") {
		t.Errorf("/tmp missing from write grants %v", p.WriteDirs)
	}
	if _, err := os.Stat("/mnt/wsl/resolv.conf"); err == nil && !contains(p.ReadFiles, "/mnt/wsl/resolv.conf") {
		t.Errorf("/mnt/wsl/resolv.conf missing from read grants %v", p.ReadFiles)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home dir")
	}
	for _, denied := range []string{filepath.Join(home, ".ssh"), filepath.Join(home, ".aws"), filepath.Join(home, ".zarlcode")} {
		if contains(p.ReadDirs, denied) || contains(p.WriteDirs, denied) {
			t.Errorf("%q must not be granted", denied)
		}
	}
}

func TestDefaultPolicyWorktree(t *testing.T) {
	main := t.TempDir()
	gitDir := filepath.Join(main, ".git")
	if err := os.MkdirAll(filepath.Join(gitDir, "worktrees", "wt"), 0o755); err != nil {
		t.Fatal(err)
	}
	wt := t.TempDir()
	pointer := "gitdir: " + filepath.Join(gitDir, "worktrees", "wt") + "\n"
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte(pointer), 0o644); err != nil {
		t.Fatal(err)
	}
	p := sandbox.DefaultPolicy(wt)
	if !contains(p.WriteDirs, gitDir) {
		t.Errorf("linked worktree should grant main .git %q, got %v", gitDir, p.WriteDirs)
	}
}

func TestPolicyWithExecPath(t *testing.T) {
	p := sandbox.DefaultPolicy(t.TempDir()).WithExecPath(`/mnt/c/Program Files/Google/Chrome/Application/chrome.exe`)
	if !contains(p.ReadFiles, `/mnt/c/Program Files/Google/Chrome/Application/chrome.exe`) {
		t.Fatalf("exec path missing from read files: %v", p.ReadFiles)
	}
	for _, dir := range []string{
		`/mnt`,
		`/mnt/c`,
		`/mnt/c/Program Files`,
		`/mnt/c/Program Files/Google`,
		`/mnt/c/Program Files/Google/Chrome`,
		`/mnt/c/Program Files/Google/Chrome/Application`,
	} {
		if !contains(p.ReadDirs, dir) {
			t.Fatalf("ancestor dir %q missing from read dirs: %v", dir, p.ReadDirs)
		}
	}
	if _, err := os.Stat("/proc/sys/fs/binfmt_misc/WSLInterop"); err == nil {
		if !contains(p.ReadFiles, "/init") {
			t.Fatalf("wsl interop interpreter /init missing from read files: %v", p.ReadFiles)
		}
	}
}

func contains(paths []string, p string) bool {
	for _, e := range paths {
		if e == p {
			return true
		}
	}
	return false
}
