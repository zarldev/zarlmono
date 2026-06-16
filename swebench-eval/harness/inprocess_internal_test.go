package harness

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/swebench-eval/internal/evaluator"
	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	"github.com/zarldev/zarlmono/zkit/agent/pursue"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestRenderSystemPromptIncludesToolsAndWorkspace(t *testing.T) {
	root := t.TempDir()
	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	reg := tools.NewRegistry()
	coderunner.RegisterStandardTools(reg, ws, nil)

	prompt, err := renderSystemPrompt(ws.Root(), reg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if prompt == "" {
		t.Fatal("rendered prompt is empty")
	}
	// The tool inventory must reach the model — the read tool is the
	// canonical probe (every standard set registers it).
	if !strings.Contains(prompt, string(code.ToolNameRead)) {
		t.Errorf("prompt does not mention the read tool:\n%s", prompt)
	}
	if !strings.Contains(prompt, ws.Root()) {
		t.Errorf("prompt does not mention the workspace root %q", ws.Root())
	}
}

func TestVerifiedFeedbackIncludesFailToPass(t *testing.T) {
	msg := verifiedFeedback(Task{FailToPass: []string{"pkg.TestThing"}}, pursue.Attempt{Number: 2}, "still red")
	if !strings.Contains(msg, "still red") || !strings.Contains(msg, "pkg.TestThing") {
		t.Fatalf("feedback missing verifier reason or fail-to-pass names:\n%s", msg)
	}
	if !strings.Contains(msg, "production/source files only") || !strings.Contains(msg, "do not edit tests or fixtures") {
		t.Fatalf("feedback should steer away from test edits:\n%s", msg)
	}
}

func TestSwebenchContextThreaderDropsTranscript(t *testing.T) {
	next := swebenchContextThreader(t.Context(), pursue.Attempt{}, runner.TaskSpec{
		Prompt:  "old",
		Context: []llm.Message{{Role: llm.RoleAssistant, Content: "large transcript"}},
	}, pursue.Retry("verifier feedback"))
	if next.Prompt != "verifier feedback" {
		t.Fatalf("Prompt = %q, want verifier feedback", next.Prompt)
	}
	if len(next.Context) != 0 {
		t.Fatalf("Context = %+v, want dropped transcript", next.Context)
	}
}

func TestParseVerifySummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "summary.json")
	body, err := json.Marshal(evaluator.Summary{
		ResolvedIDs:   []string{"ok"},
		UnresolvedIDs: []string{"bad"},
		EmptyPatchIDs: []string{"empty"},
		ErrorIDs:      []string{"err"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := evaluator.ParseSummary(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !got["ok"].Resolved {
		t.Fatalf("ok verdict = %+v, want resolved", got["ok"])
	}
	if got["bad"].Resolved || !errors.Is(got["empty"].Err, evaluator.ErrEmptyPatch) ||
		!errors.Is(got["err"].Err, evaluator.ErrEvaluatorError) {
		t.Fatalf("unexpected verdicts: %+v", got)
	}
}

func TestTaskPromptAppendsHints(t *testing.T) {
	got := taskPrompt(Task{Problem: "fix the bug", Hints: "look in foo.go"})
	if !strings.Contains(got, "fix the bug") {
		t.Errorf("task prompt missing problem: %q", got)
	}
	if !strings.Contains(got, "look in foo.go") {
		t.Errorf("task prompt missing hints: %q", got)
	}

	bare := taskPrompt(Task{Problem: "just the problem"})
	if strings.Contains(bare, "Hints") {
		t.Errorf("bare task prompt should not carry a Hints section: %q", bare)
	}
}

func TestCountToolCalls(t *testing.T) {
	msgs := []llm.Message{
		{Role: "user", Content: "go"},
		{Role: roleAssistant, ToolCalls: []llm.ToolCall{{ID: "a"}, {ID: "b"}}},
		{Role: "tool", ToolCallID: "a"},
		{Role: roleAssistant, ToolCalls: []llm.ToolCall{{ID: "c"}}},
		{Role: roleAssistant, Content: "done"}, // final, no calls
	}
	if got := coderunner.ToolCallCount(msgs); got != 3 {
		t.Errorf("ToolCallCount = %d, want 3", got)
	}
}

func TestCaptureWorktreeDiffSeesTrackedAndUntracked(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "eval@test")
	run("config", "user.name", "eval")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "base")

	// Snapshot the clean state, then mutate a tracked file + create a
	// new file — exactly the shape an agent leaves behind.
	pre := code.UntrackedFiles(t.Context(), root)
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "added.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	diff := code.WorktreeDiff(t.Context(), root, "", pre)
	if !strings.Contains(diff, "tracked.txt") {
		t.Errorf("diff missing tracked change:\n%s", diff)
	}
	if !strings.Contains(diff, "added.txt") {
		t.Errorf("diff missing untracked new file:\n%s", diff)
	}
	if !strings.Contains(diff, "+v2") {
		t.Errorf("diff missing the edit content:\n%s", diff)
	}
}
