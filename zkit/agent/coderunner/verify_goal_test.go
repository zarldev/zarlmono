package coderunner_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	"github.com/zarldev/zarlmono/zkit/agent/pursue"
)

// touch writes a file under root so the worktree-state snapshot changes
// between attempts (the goal's changed-nothing guard keys off it).
func touch(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatalf("touch %s: %v", name, err)
	}
}

// dirState is a worktreeState fake: the concatenated names+sizes of files in
// root — cheap and deterministic, like the git snapshot in production.
func dirState(root string) func() string {
	return func() string {
		entries, _ := os.ReadDir(root)
		var b strings.Builder
		for _, e := range entries {
			info, _ := e.Info()
			b.WriteString(e.Name())
			if info != nil {
				b.WriteString(info.ModTime().String())
				b.WriteByte(byte(info.Size()))
			}
		}
		return b.String()
	}
}

func TestCommandGoalPassFailCycle(t *testing.T) {
	root := t.TempDir()
	// The oracle passes only once marker exists — the agent's "fix".
	goal := coderunner.CommandGoal(root, "test -f marker", dirState(root), coderunner.VerifyOpts{})

	// Attempt 1: the agent edited (state changed) but the fix is wrong.
	touch(t, root, "wrong.go", "attempt 1")
	d := goal.Evaluate(t.Context(), pursue.Attempt{Number: 1})
	if d.Done {
		t.Fatal("attempt 1: Done, want retry (oracle fails)")
	}
	if !strings.Contains(d.Feedback, "Verification failed") || !strings.Contains(d.Feedback, "test -f marker") {
		t.Errorf("attempt 1 feedback missing failure framing: %q", d.Feedback)
	}

	// Attempt 2: the agent landed the fix.
	touch(t, root, "marker", "fixed")
	d = goal.Evaluate(t.Context(), pursue.Attempt{Number: 2})
	if !d.Done {
		t.Fatalf("attempt 2: retry (%q), want Done", d.Feedback)
	}
}

func TestCommandGoalChangedNothingGuard(t *testing.T) {
	root := t.TempDir()
	// Count oracle invocations via an append-marker file OUTSIDE root so it
	// doesn't perturb the state snapshot.
	countFile := filepath.Join(t.TempDir(), "runs")
	goal := coderunner.CommandGoal(root, "echo x >> "+countFile+"; exit 1", dirState(root), coderunner.VerifyOpts{})

	// Attempt 1 with NO changes since construction: the guard fires before
	// the oracle runs — the empty-patch class from eval.
	d := goal.Evaluate(t.Context(), pursue.Attempt{Number: 1})
	if d.Done || !strings.Contains(d.Feedback, "no changes") {
		t.Fatalf("unchanged attempt: done=%v feedback=%q, want no-changes retry", d.Done, d.Feedback)
	}
	if _, err := os.Stat(countFile); !os.IsNotExist(err) {
		t.Error("oracle ran despite unchanged workspace")
	}

	// A real change runs the oracle; its failure feedback is preserved and
	// resurfaces on a subsequent do-nothing attempt.
	touch(t, root, "a.go", "edit")
	d = goal.Evaluate(t.Context(), pursue.Attempt{Number: 2})
	if d.Done {
		t.Fatal("failing oracle reported Done")
	}
	data, err := os.ReadFile(countFile)
	if err != nil || strings.Count(string(data), "x") != 1 {
		t.Fatalf("oracle run count = %q (err %v), want exactly one run", data, err)
	}
	d = goal.Evaluate(t.Context(), pursue.Attempt{Number: 3})
	if d.Done || !strings.Contains(d.Feedback, "previous verification failure") {
		t.Errorf("repeat do-nothing attempt: done=%v feedback=%q, want carried failure", d.Done, d.Feedback)
	}
}

func TestCommandGoalFeedbackCarriesOutputTail(t *testing.T) {
	root := t.TempDir()
	goal := coderunner.CommandGoal(root, "echo 'FAIL: TestThing expected 4 got 5'; exit 1", nil, coderunner.VerifyOpts{})
	d := goal.Evaluate(t.Context(), pursue.Attempt{Number: 1})
	if d.Done {
		t.Fatal("failing command reported Done")
	}
	if !strings.Contains(d.Feedback, "TestThing expected 4 got 5") {
		t.Errorf("feedback lost the oracle output: %q", d.Feedback)
	}
}
