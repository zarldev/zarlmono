package releasecheck

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// CheckSource rejects dirty candidates, verification for another commit, and
// candidates that do not contain the explicitly verified upstream commit.
// It performs no network access: the caller must freshly verify upstream and
// pass its full commit ID, rather than trusting a cached tracking reference.
func CheckSource(ctx context.Context, root, upstream, candidate string) error {
	for _, id := range []string{upstream, candidate} {
		if len(id) != 40 && len(id) != 64 {
			return errors.New("source provenance requires full commit IDs")
		}
		for _, char := range id {
			if !strings.ContainsRune("0123456789abcdef", char) {
				return errors.New("source provenance requires lowercase hexadecimal commit IDs")
			}
		}
	}
	git := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = root
		data, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("source git %s: %w", args[0], err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	head, err := git("rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if head != candidate {
		return fmt.Errorf("verification is for %s, candidate HEAD is %s", candidate, head)
	}
	status, err := git("status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return err
	}
	if status != "" {
		return errors.New("release source has uncommitted changes")
	}
	if _, err := git("merge-base", "--is-ancestor", upstream, candidate); err != nil {
		return fmt.Errorf("candidate does not include verified upstream %s: %w", upstream, err)
	}
	return nil
}
