package runner_test

import (
	"context"
	"iter"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// --- unit tests for RequireWork.Inspect ---

func TestRequireWork_HoldsWhenNoWorkDone(t *testing.T) {
	t.Parallel()
	got := runner.RequireWork{}.Inspect(false, "I think the fix is obvious.")
	if got.Correction == "" {
		t.Error("Inspect(workDone=false): want non-empty correction")
	}
}

func TestRequireWork_QuietWhenWorkDone(t *testing.T) {
	t.Parallel()
	if got := (runner.RequireWork{}).Inspect(true, ""); got.Correction != "" {
		t.Errorf("Inspect(workDone=true): want empty correction, got %q", got.Correction)
	}
}

func TestRequireWork_CustomMessageOverrides(t *testing.T) {
	t.Parallel()
	got := runner.RequireWork{Message: "PRODUCE THE DIFF NOW"}.Inspect(false, "")
	if got.Correction != "PRODUCE THE DIFF NOW" {
		t.Errorf("custom message lost: got %q", got.Correction)
	}
}

func TestRequireWork_PassesThroughMaxCorrections(t *testing.T) {
	t.Parallel()
	if got := (runner.RequireWork{MaxCorrections: 3}).Inspect(false, ""); got.MaxCorrections != 3 {
		t.Errorf("MaxCorrections = %d, want 3", got.MaxCorrections)
	}
}

// --- end-to-end loop behaviour ---

// noEditProvider emits a text-only "done" every turn and never calls a
// tool — the confident-no-op shape that completes with an empty patch.
type noEditProvider struct {
	requests atomic.Int32
}

func (p *noEditProvider) Complete(_ context.Context, _ llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	p.requests.Add(1)
	return func(yield func(llm.CompletionChunk, error) bool) {
		yield(llm.CompletionChunk{Content: "All done — the fix looks correct."}, nil)
	}, nil
}

func (p *noEditProvider) Name() string { return "no-edit" }

// editThenDoneProvider calls a mutating tool on turn 1, then emits a
// text-only "done" on turn 2 — the well-behaved shape the gate must let
// through untouched.
type editThenDoneProvider struct {
	iter atomic.Int32
}

func (p *editThenDoneProvider) Complete(_ context.Context, _ llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	return func(yield func(llm.CompletionChunk, error) bool) {
		if p.iter.Add(1) == 1 {
			yield(llm.CompletionChunk{ToolCalls: []llm.ToolCall{{
				ID:       "tc-edit",
				Type:     "function",
				Function: llm.ToolCallFunction{Name: "edit", Arguments: `{"x":1}`},
			}}}, nil)
			return
		}
		yield(llm.CompletionChunk{Content: "Done — change applied."}, nil)
	}, nil
}

func (p *editThenDoneProvider) Name() string { return "edit-then-done" }

// mutatingTool reports Mutates=true and always succeeds — stands in for
// edit / write / apply_patch.
type mutatingTool struct{ name string }

func (t *mutatingTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        tools.ToolName(t.name),
		Description: "mutating",
		Parameters:  llm.Schema{Type: "object", AdditionalProperties: true},
		Mutates:     true,
	}
}

func (t *mutatingTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	return &tools.ToolResult{ToolCallID: call.ID, Success: true, Data: "edited"}, nil
}

func TestRunner_CompletionGateHoldsThenTerminatesAtBudget(t *testing.T) {
	t.Parallel()
	prov := &noEditProvider{}
	r := runner.New(
		runner.ClientFromProvider(prov),
		runner.WithTools(tools.NewRegistry()),
		runner.WithMaxIterations(10),
		runner.WithCompletionGate(runner.RequireWork{MaxCorrections: 2}),
	)
	res := r.Run(t.Context(), runner.TaskSpec{ID: taskscope.ID(uuid.NewString()), Prompt: "go"})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	// The model never edits, so the gate holds twice (MaxCorrections=2)
	// then lets the third no-tool-call turn complete: 3 provider calls.
	if got := prov.requests.Load(); got != 3 {
		t.Errorf("provider calls = %d, want 3 (initial + 2 gate holds)", got)
	}
	if res.Reason != runner.TerminalCompleted {
		t.Errorf("Reason = %q, want completed once the gate budget is spent", res.Reason)
	}
	// Exactly two corrective user turns landed in canonical history.
	if n := countWorkCorrections(res.Messages); n != 2 {
		t.Errorf("injected corrections in history = %d, want 2", n)
	}
}

func TestRunner_CompletionGateLetsThroughWhenWorkDone(t *testing.T) {
	t.Parallel()
	reg := tools.NewRegistry()
	reg.Register(&mutatingTool{name: "edit"})

	r := runner.New(
		runner.ClientFromProvider(&editThenDoneProvider{}),
		runner.WithTools(reg),
		runner.WithMaxIterations(10),
		runner.WithCompletionGate(runner.RequireWork{MaxCorrections: 2}),
	)
	res := r.Run(t.Context(), runner.TaskSpec{ID: taskscope.ID(uuid.NewString()), Prompt: "go"})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if res.Reason != runner.TerminalCompleted {
		t.Fatalf("Reason = %q, want completed", res.Reason)
	}
	// The mutating edit on turn 1 satisfies the gate, so turn 2's
	// text-only reply completes with no correction injected.
	if n := countWorkCorrections(res.Messages); n != 0 {
		t.Errorf("corrections injected = %d, want 0 (work was done)", n)
	}
	if !strings.Contains(res.FinalContent, "change applied") {
		t.Errorf("FinalContent = %q, want turn-2 reply", res.FinalContent)
	}
}

func TestRunner_NoCompletionGateExitsImmediatelyWithoutWork(t *testing.T) {
	t.Parallel()
	// Without the gate, the first text-only turn ends the loop even
	// though nothing was edited — the pre-existing behaviour must be
	// preserved for consumers that don't opt in.
	prov := &noEditProvider{}
	r := runner.New(
		runner.ClientFromProvider(prov),
		runner.WithTools(tools.NewRegistry()),
		runner.WithMaxIterations(10),
		// no WithCompletionGate
	)
	res := r.Run(t.Context(), runner.TaskSpec{ID: taskscope.ID(uuid.NewString()), Prompt: "go"})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if res.Iterations != 1 {
		t.Errorf("Iterations = %d, want 1 (no gate → first no-tool turn exits)", res.Iterations)
	}
	if got := prov.requests.Load(); got != 1 {
		t.Errorf("provider calls = %d, want 1", got)
	}
}

// countWorkCorrections counts injected user-side gate corrections by the
// signature phrase from defaultRequireWorkMessage.
func countWorkCorrections(messages []llm.Message) int {
	n := 0
	for _, m := range messages {
		if m.Role == llm.RoleUser && strings.Contains(m.Content, "empty patch") {
			n++
		}
	}
	return n
}
