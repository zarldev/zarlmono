package tui_test

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/askpass"
	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestAskpassServerIgnoresCheckoutSymlinks(t *testing.T) {
	for _, target := range []string{"helper", "run", "config"} {
		t.Run(target, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			victim := filepath.Join(outside, "askpass.sh")
			const original = "preserve this file\n"
			if err := os.WriteFile(victim, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			run := filepath.Join(root, ".zarlcode", "run")
			link, destination := filepath.Join(run, "askpass.sh"), victim
			switch target {
			case "run":
				link, destination = run, outside
			case "config":
				link, destination = filepath.Dir(run), outside
				if err := os.Mkdir(filepath.Join(outside, "run"), 0o700); err != nil {
					t.Fatal(err)
				}
				victim = filepath.Join(outside, "run", "askpass.sh")
				if err := os.WriteFile(victim, []byte(original), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(destination, link); err != nil {
				t.Fatal(err)
			}
			server, err := tui.NewAskpassServer(t.Context(), root)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = server.Close() })
			if err := server.Close(); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(victim)
			if err != nil || string(got) != original {
				t.Fatalf("checkout symlink target changed: %q, %v", got, err)
			}
			if got, err := os.Readlink(link); err != nil || got != destination {
				t.Fatalf("checkout symlink changed: %q, %v", got, err)
			}
		})
	}
}

func TestAskpassServerOwnsPrivateInstanceArtifacts(t *testing.T) {
	root := t.TempDir()
	first, err := tui.NewAskpassServer(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := tui.NewAskpassServer(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	firstEnv, secondEnv := first.Env(), second.Env()
	if firstEnv[askpass.EnvSock] == secondEnv[askpass.EnvSock] || firstEnv["SUDO_ASKPASS"] == secondEnv["SUDO_ASKPASS"] {
		t.Fatal("askpass instances share runtime paths")
	}
	for _, env := range []map[string]string{firstEnv, secondEnv} {
		dir := filepath.Dir(env["SUDO_ASKPASS"])
		if rel, err := filepath.Rel(root, dir); err != nil || filepath.IsLocal(rel) {
			t.Fatalf("runtime directory must be outside checkout: %q, %v", dir, err)
		}
		for path, mode := range map[string]os.FileMode{dir: 0o700, env["SUDO_ASKPASS"]: 0o700, env[askpass.EnvSock]: 0o600} {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != mode {
				t.Errorf("%s mode = %o, want %o", path, info.Mode().Perm(), mode)
			}
		}
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(firstEnv["SUDO_ASKPASS"])); !os.IsNotExist(err) {
		t.Fatalf("closed instance directory remains: %v", err)
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(t.Context(), "unix", secondEnv[askpass.EnvSock])
	if err != nil {
		t.Fatalf("closing first instance broke second: %v", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(conn).Encode(askpass.Request{Prompt: "test"}); err != nil {
		t.Fatal(err)
	}
	var response askpass.Response
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		t.Fatalf("second instance did not respond: %v", err)
	}
	if response.Error != "askpass UI is not ready" {
		t.Fatalf("unexpected response: %+v", response)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(secondEnv["SUDO_ASKPASS"])); !os.IsNotExist(err) {
		t.Fatalf("second instance directory remains: %v", err)
	}
}

func TestAskpassServerRejectsWorkspaceTempDirectory(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "symlink"}[symlink], func(t *testing.T) {
			root := t.TempDir()
			temp := root
			if symlink {
				temp = filepath.Join(t.TempDir(), "temp-link")
				if err := os.Symlink(root, temp); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("TMPDIR", temp)
			server, err := tui.NewAskpassServer(t.Context(), root)
			if err == nil {
				_ = server.Close()
				t.Fatal("accepted a workspace-owned temporary directory")
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("rejected startup changed workspace: %v, %v", entries, err)
			}
		})
	}
}

func TestAskpassServerCleansRuntimeDirectoryOnListenFailure(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(t.TempDir(), strings.Repeat("x", 110))
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", parent)
	server, err := tui.NewAskpassServer(t.Context(), root)
	if err == nil {
		_ = server.Close()
		t.Fatal("expected Unix socket path length failure")
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed construction left runtime artifacts: %v, %v", entries, err)
	}
}
