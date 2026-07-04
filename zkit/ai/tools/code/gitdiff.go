package code

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// GitHead returns the workspace's current HEAD commit, or "" when the
// workspace isn't a git repository. Capture it before a run starts so a
// HEAD the agent moves mid-run doesn't shift the diff baseline.
func GitHead(ctx context.Context, workspace string) string {
	out, err := exec.CommandContext(ctx, "git", "-C", workspace, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// UntrackedFiles returns the set of untracked paths git reports for the
// workspace. Capture it before a run and pass it to [WorktreeDiff] as
// exclude so pre-existing untracked files don't count as agent changes.
// Empty map when the workspace isn't a git repository.
func UntrackedFiles(ctx context.Context, workspace string) map[string]bool {
	set := map[string]bool{}
	for _, p := range untrackedPaths(ctx, workspace) {
		set[p] = true
	}
	return set
}

// WorktreeDiff returns the unified diff of the workspace against base
// ("" means HEAD): tracked changes from `git diff base`, plus a
// synthesized /dev/null diff for each untracked file not in exclude
// (plain diff omits untracked files). Best-effort by design — it
// returns "" when nothing changed or git is unavailable, and a failed
// tracked-file diff doesn't stop untracked capture.
func WorktreeDiff(ctx context.Context, workspace, base string, exclude map[string]bool) string {
	if base == "" {
		base = "HEAD"
	}
	var b strings.Builder
	if out, err := exec.CommandContext(ctx, "git", "-C", workspace, "diff", base).Output(); err == nil {
		b.Write(out)
	}
	for _, path := range untrackedPaths(ctx, workspace) {
		if exclude[path] {
			continue
		}
		if extra, err := untrackedDiff(ctx, workspace, path); err == nil {
			b.WriteString(extra)
		}
	}
	return b.String()
}

// untrackedPaths returns untracked files in git's reporting order, so
// appended diffs are deterministic. Nil when the workspace isn't a git
// repository.
func untrackedPaths(ctx context.Context, workspace string) []string {
	out, err := exec.CommandContext(ctx, "git", "-C", workspace, "ls-files", "--others", "--exclude-standard").Output()
	if err != nil {
		return nil
	}
	var paths []string
	for p := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// untrackedDiff synthesizes a /dev/null → path unified diff for one new
// file. `git diff --no-index` exits 1 when the files differ — that's
// the success path for "new file", so an exit-1 with output is treated
// as a clean result.
func untrackedDiff(ctx context.Context, workspace, path string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", workspace, "diff", "--no-index", "/dev/null", path).Output()
	if err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok && exitErr.ExitCode() == 1 && len(out) > 0 {
			return string(out), nil
		}
		return "", err
	}
	return string(out), nil
}
