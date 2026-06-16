//go:build linux

package sandbox_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/sandbox"
)

// TestDefaultPolicyWSLResolvConfGrant verifies that when WSL's resolver file
// exists, the default sandbox policy grants read access to that exact file so
// hostname resolution survives the /etc/resolv.conf symlink.
func TestDefaultPolicyWSLResolvConfGrant(t *testing.T) {
	if _, err := os.Stat("/mnt/wsl/resolv.conf"); err != nil {
		t.Skip("/mnt/wsl/resolv.conf not present on this host")
	}
	p := sandbox.DefaultPolicy(t.TempDir())
	want := "/mnt/wsl/resolv.conf"
	found := false
	for _, f := range p.ReadFiles {
		if filepath.Clean(f) == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("default policy missing %q in ReadFiles: %v", want, p.ReadFiles)
	}
}
