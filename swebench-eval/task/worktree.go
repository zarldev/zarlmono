package task

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Materialize prepares a worktree for one task at parent/<instance_id>.
// Steps:
//
//  1. If a cached clone of the target repo exists in cacheDir, use it
//     as a local reference for the new clone (fast, avoids re-fetching
//     gigabytes for repos like Django). Otherwise clone from
//     github.com.
//  2. Check out the spec's base commit. SWE-bench tasks pin a specific
//     SHA — the harness must see the project exactly as it was before
//     the bug fix.
//  3. Configure a local git identity (user.name / user.email) so the
//     agent's commits, if any, succeed inside the worktree.
//  4. Return the absolute path to the prepared worktree.
//
// Materialize is idempotent at the directory level: if the target dir
// already exists and looks like a valid clone, we update + reset
// rather than wiping and re-cloning. That lets a rerun pick up where
// it left off (or rebuild a corrupted worktree by deleting the dir
// manually before invoking again).
//
// Returns the worktree path on success. On failure, removes any
// partially-created worktree so the next attempt starts clean.
func Materialize(ctx context.Context, s Spec, parent, cacheDir string) (string, error) {
	wt := filepath.Join(parent, s.InstanceID)
	repoURL := "https://github.com/" + s.Repo

	if _, err := os.Stat(filepath.Join(wt, ".git")); err == nil {
		// Existing worktree: reset to base commit. Cheaper than re-cloning.
		if err := runGit(ctx, wt, "fetch", "--depth=1", "origin", s.BaseCommit); err != nil {
			_ = os.RemoveAll(wt)
			// fall through to fresh clone
		} else {
			if err := runGit(ctx, wt, "reset", "--hard", s.BaseCommit); err != nil {
				_ = os.RemoveAll(wt)
			} else {
				if err := runGit(ctx, wt, "clean", "-fdx"); err != nil {
					_ = os.RemoveAll(wt)
				} else {
					return wt, configureIdentity(ctx, wt)
				}
			}
		}
	}

	if err := os.MkdirAll(parent, 0o750); err != nil {
		return "", fmt.Errorf("mkdir worktree parent: %w", err)
	}

	args := []string{"clone"}
	cached := cachedClonePath(cacheDir, s.Repo)
	if cached != "" {
		// `git clone --reference` shares object storage with the cache,
		// turning a multi-GB clone into a few-MB metadata copy. The
		// --dissociate flag promotes shared objects to local once
		// cloning succeeds, so deleting the cache later doesn't
		// corrupt the worktree.
		args = append(args, "--reference-if-able", cached, "--dissociate")
	}
	args = append(args, repoURL, wt)
	if err := runGit(ctx, "", args...); err != nil {
		_ = os.RemoveAll(wt)
		return "", fmt.Errorf("clone %s: %w", s.Repo, err)
	}
	if err := runGit(ctx, wt, "checkout", "--detach", s.BaseCommit); err != nil {
		_ = os.RemoveAll(wt)
		return "", fmt.Errorf("checkout %s: %w", s.BaseCommit, err)
	}
	if err := configureIdentity(ctx, wt); err != nil {
		_ = os.RemoveAll(wt)
		return "", err
	}
	return wt, nil
}

// configureIdentity sets a deterministic git author for the worktree
// so commits the agent makes don't pick up the host user's
// global git config (which might bleed into a submitted patch's
// metadata for harnesses that commit before diffing).
func configureIdentity(ctx context.Context, wt string) error {
	if err := runGit(ctx, wt, "config", "user.name", "swebench-eval"); err != nil {
		return err
	}
	return runGit(ctx, wt, "config", "user.email", "eval@swebench.local")
}

// cachedClonePath returns the on-disk location of a cached clone of
// the named repo, or "" when cacheDir is empty / the cache doesn't
// have this repo yet. Caller passes the return value to git clone
// --reference-if-able so a missing cache is graceful.
func cachedClonePath(cacheDir, repo string) string {
	if cacheDir == "" {
		return ""
	}
	p := filepath.Join(cacheDir, repo)
	if _, err := os.Stat(filepath.Join(p, ".git")); err != nil {
		return ""
	}
	return p
}

func runGit(ctx context.Context, cwd string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %v: %w\n%s", args, err, out)
	}
	return nil
}
