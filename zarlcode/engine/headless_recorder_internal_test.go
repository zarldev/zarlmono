package engine

import (
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	"github.com/zarldev/zarlmono/zkit/agent/pursue"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestHeadlessRecorder_PersistsLifecycle(t *testing.T) {
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := t.Context()
	rec := &headlessRecorder{store: store, id: "run-1", workspace: t.TempDir()}

	rec.start(ctx, "fix the bug", "llamacpp", "qwen3.6")
	got, err := store.GetHeadlessRun(ctx, "run-1")
	if err != nil {
		t.Fatalf("get after start: %v", err)
	}
	if got.Prompt != "fix the bug" || got.Provider != "llamacpp" || got.Model != "qwen3.6" {
		t.Errorf("start row = %+v; want prompt/provider/model populated", got)
	}
	if got.EndedAt != nil {
		t.Error("a freshly started run must have a nil EndedAt")
	}

	// Progress persists counters WITHOUT completing the run — the
	// SIGKILL guarantee: a killed process leaves the last progress but no
	// terminal state.
	rec.progress(ctx, 3, 7)
	got, _ = store.GetHeadlessRun(ctx, "run-1")
	if got.Iterations != 3 || got.ToolCalls != 7 {
		t.Errorf("after progress: iters=%d tools=%d; want 3/7", got.Iterations, got.ToolCalls)
	}
	if got.EndedAt != nil {
		t.Error("progress must not mark the run ended")
	}

	in := int64(100)
	rec.complete(ctx, runner.TaskResult{
		Reason:       runner.TerminalCompleted,
		FinalContent: "all done",
		Iterations:   4,
		TotalUsage:   &llm.Usage{PromptTokens: 100, CompletionTokens: 50},
	})
	got, _ = store.GetHeadlessRun(ctx, "run-1")
	if got.EndedAt == nil {
		t.Error("a completed run must have a non-nil EndedAt")
	}
	if got.TerminalReason != string(runner.TerminalCompleted) {
		t.Errorf("terminal reason = %q; want completed", got.TerminalReason)
	}
	if got.Iterations != 4 {
		t.Errorf("completed iterations = %d; want 4", got.Iterations)
	}
	if got.FinalContent != "all done" {
		t.Errorf("final content = %q", got.FinalContent)
	}
	if got.TokensIn == nil || *got.TokensIn != in {
		t.Errorf("tokens in = %v; want %d", got.TokensIn, in)
	}
}

func TestHeadlessRecorder_PersistsAttempt(t *testing.T) {
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := t.Context()
	rec := &headlessRecorder{store: store, id: "run-attempt", workspace: t.TempDir()}
	rec.start(ctx, "fix the bug", "llamacpp", "qwen3.6")
	rec.attempt(ctx, pursue.AttemptReport{
		Attempt: pursue.Attempt{
			Number: 1,
			Spec:   runner.TaskSpec{Prompt: "fix the bug"},
			Result: runner.TaskResult{
				Reason:       runner.TerminalCompleted,
				FinalContent: "not yet",
				Iterations:   2,
				TotalUsage:   &llm.Usage{PromptTokens: 11, CompletionTokens: 7},
			},
		},
		Decision: pursue.Retry("tests still fail"),
	})

	attempts, err := store.ListHeadlessAttempts(ctx, "run-attempt")
	if err != nil {
		t.Fatalf("ListHeadlessAttempts: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("got %d attempts, want 1", len(attempts))
	}
	got := attempts[0]
	if got.AttemptNumber != 1 || got.Prompt != "fix the bug" || got.Feedback != "tests still fail" || got.DecisionDone {
		t.Errorf("attempt = %+v", got)
	}
	if got.TokensIn == nil || *got.TokensIn != 11 || got.TokensOut == nil || *got.TokensOut != 7 {
		t.Errorf("tokens = %v/%v", got.TokensIn, got.TokensOut)
	}
}

func TestHeadlessRecorder_PersistsVerifierResult(t *testing.T) {
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := t.Context()
	rec := &headlessRecorder{store: store, id: "run-verifier", workspace: t.TempDir()}
	rec.start(ctx, "fix the bug", "llamacpp", "qwen3.6")
	code := 7
	rec.verifierResult(ctx, coderunner.VerifyResult{
		AttemptNumber: 1,
		Command:       "go test ./...",
		Success:       false,
		ExitCode:      &code,
		Error:         "exit status 7",
		OutputTail:    "FAIL: TestThing",
	})

	results, err := store.ListHeadlessVerifierResults(ctx, "run-verifier")
	if err != nil {
		t.Fatalf("ListHeadlessVerifierResults: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d verifier results, want 1", len(results))
	}
	got := results[0]
	if got.Command != "go test ./..." || got.Success || got.OutputTail != "FAIL: TestThing" {
		t.Errorf("verifier result = %+v", got)
	}
	if got.ExitCode == nil || *got.ExitCode != 7 {
		t.Errorf("exit code = %v, want 7", got.ExitCode)
	}
}

func TestHeadlessRecorder_NilIsNoOp(t *testing.T) {
	var rec *headlessRecorder // no store configured
	// None of these must panic or touch a store.
	rec.start(t.Context(), "p", "prov", "model")
	rec.progress(t.Context(), 1, 2)
	rec.attempt(t.Context(), pursue.AttemptReport{})
	rec.complete(t.Context(), runner.TaskResult{Reason: runner.TerminalCompleted})
}
