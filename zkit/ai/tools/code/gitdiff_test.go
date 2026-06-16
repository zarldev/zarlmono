package code_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// initRepo creates a git repo with one committed file and returns its
// root. Skips the test when git isn't installed.
func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("init", "-q")
	git("add", "tracked.txt")
	git("commit", "-q", "-m", "initial")
	return root
}

func TestWorktreeDiff(t *testing.T) {
	ctx := context.Background()
	root := initRepo(t)
	base := code.GitHead(ctx, root)
	if base == "" {
		t.Fatal("GitHead returned empty commit for a git repo")
	}

	preexisting := filepath.Join(root, "preexisting.txt")
	if err := os.WriteFile(preexisting, []byte("already here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exclude := code.UntrackedFiles(ctx, root)
	if !exclude["preexisting.txt"] {
		t.Fatalf("UntrackedFiles = %v, want preexisting.txt in set", exclude)
	}

	// The "agent" edits a tracked file and creates a new one.
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "created.txt"), []byte("new file\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	diff := code.WorktreeDiff(ctx, root, base, exclude)
	for _, want := range []string{"tracked.txt", "+changed", "created.txt", "+new file", "/dev/null"} {
		if !strings.Contains(diff, want) {
			t.Errorf("WorktreeDiff missing %q:\n%s", want, diff)
		}
	}
	if strings.Contains(diff, "preexisting.txt") {
		t.Errorf("WorktreeDiff includes excluded preexisting.txt:\n%s", diff)
	}

	// Empty base falls back to HEAD — same result while HEAD == base.
	if got := code.WorktreeDiff(ctx, root, "", exclude); got != diff {
		t.Errorf("WorktreeDiff with empty base differs from explicit HEAD base")
	}
}

func TestWorktreeDiffNotARepo(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if got := code.WorktreeDiff(ctx, root, "", nil); got != "" {
		t.Errorf("WorktreeDiff on non-repo = %q, want empty", got)
	}
	if got := code.GitHead(ctx, root); got != "" {
		t.Errorf("GitHead on non-repo = %q, want empty", got)
	}
	if got := code.UntrackedFiles(ctx, root); len(got) != 0 {
		t.Errorf("UntrackedFiles on non-repo = %v, want empty", got)
	}
}
