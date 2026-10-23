package releasecheck_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/tools/releasecheck"
)

func TestCheckSourceProvenance(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		data, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, data)
		}
		return strings.TrimSpace(string(data))
	}
	git("init", "-q")
	git("-c", "commit.gpgsign=false", "commit", "--allow-empty", "-qm", "base")
	base := git("rev-parse", "HEAD")
	if err := releasecheck.CheckSource(t.Context(), root, base, base); err != nil {
		t.Fatal(err)
	}
	git("-c", "commit.gpgsign=false", "commit", "--allow-empty", "-qm", "candidate")
	candidate := git("rev-parse", "HEAD")
	if err := releasecheck.CheckSource(t.Context(), root, base, base); err == nil {
		t.Fatal("stale verification accepted")
	}
	if err := releasecheck.CheckSource(t.Context(), root, base, candidate); err != nil {
		t.Fatal(err)
	}
	git("checkout", "--detach", base)
	if err := releasecheck.CheckSource(t.Context(), root, candidate, base); err == nil {
		t.Fatal("stale upstream base accepted")
	}
	git("checkout", "--detach", candidate)
	if err := os.WriteFile(filepath.Join(root, "unexpected"), []byte("dirty"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := releasecheck.CheckSource(t.Context(), root, base, candidate); err == nil {
		t.Fatal("untracked change accepted")
	}
	git("add", "unexpected")
	if err := releasecheck.CheckSource(t.Context(), root, base, candidate); err == nil {
		t.Fatal("staged change accepted")
	}
	if err := releasecheck.CheckSource(t.Context(), root, "origin/main", candidate); err == nil {
		t.Fatal("mutable reference accepted")
	}
}
