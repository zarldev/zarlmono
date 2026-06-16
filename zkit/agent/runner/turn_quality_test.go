package runner_test

import (
	"context"
	"iter"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestEmptyResponseDetector_FiresOnEmptyContentAndNoTools(t *testing.T) {
	t.Parallel()
	got := runner.EmptyResponseDetector{}.Inspect("", nil)
	if got.Correction == "" {
		t.Error("Inspect('', nil): want non-empty correction")
	}
}

func TestEmptyResponseDetector_QuietOnNonEmptyContent(t *testing.T) {
	t.Parallel()
	if got := (runner.EmptyResponseDetector{}).Inspect("here is my answer", nil); got.Correction != "" {
		t.Errorf("Inspect with content: want empty, got %q", got.Correction)
	}
}

func TestEmptyResponseDetector_QuietOnToolCalls(t *testing.T) {
	t.Parallel()
	calls := []llm.ToolCall{{ID: "tc-1", Function: llm.ToolCallFunction{Name: "read"}}}
	if got := (runner.EmptyResponseDetector{}).Inspect("", calls); got.Correction != "" {
		t.Errorf("Inspect with tool call: want empty, got %q", got.Correction)
	}
}

func TestEmptyResponseDetector_TrimsWhitespaceBeforeChecking(t *testing.T) {
	t.Parallel()
	// A turn that emitted only whitespace (or just newlines from a
	// stripped thinking block) is still empty for routing purposes.
	if got := (runner.EmptyResponseDetector{}).Inspect("  \n\t\n  ", nil); got.Correction == "" {
		t.Error("whitespace-only content: want correction, got empty")
	}
}

func TestEmptyResponseDetector_CustomMessageOverrides(t *testing.T) {
	t.Parallel()
	d := runner.EmptyResponseDetector{Message: "say something useful"}
	got := d.Inspect("", nil)
	if got.Correction != "say something useful" {
		t.Errorf("custom message lost: got %q", got.Correction)
	}
}

func TestEmptyResponseDetector_DisableThinkingDecision(t *testing.T) {
	t.Parallel()
	d := runner.EmptyResponseDetector{DisableThinkingOnRetry: true, MaxCorrections: 1}
	got := d.Inspect("", nil)
	if !got.DisableThinking {
		t.Fatal("DisableThinking = false, want true")
	}
	if got.MaxCorrections != 1 {
		t.Fatalf("MaxCorrections = %d, want 1", got.MaxCorrections)
	}
}

// emptyTurnProvider emits an empty turn (no content, no tool calls)
// on iteration 1, then a real answer on iteration 2. Used to verify
// the runner's TurnQuality hook injects the follow-up and continues
// rather than terminating after the empty turn.
type emptyTurnProvider struct {
	iter atomic.Int32
}

func (p *emptyTurnProvider) Complete(_ context.Context, _ llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	return func(yield func(llm.CompletionChunk, error) bool) {
		switch p.iter.Add(1) {
		case 1:
			// Empty turn: zero content, zero tool calls.
			yield(llm.CompletionChunk{}, nil)
		default:
			yield(llm.CompletionChunk{Content: "done now"}, nil)
		}
	}, nil
}

func (p *emptyTurnProvider) Name() string { return "empty-turn" }

type recordingEmptyTurnProvider struct {
	mu       sync.Mutex
	requests []llm.CompletionRequest
	iter     int
}

func (p *recordingEmptyTurnProvider) Complete(_ context.Context, req llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.iter++
	turn := p.iter
	p.mu.Unlock()

	return func(yield func(llm.CompletionChunk, error) bool) {
		if turn == 1 {
			yield(llm.CompletionChunk{}, nil)
			return
		}
		yield(llm.CompletionChunk{Content: "visible summary"}, nil)
	}, nil
}

func (p *recordingEmptyTurnProvider) Name() string { return "recording-empty-turn" }

func (p *recordingEmptyTurnProvider) snapshotRequests() []llm.CompletionRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]llm.CompletionRequest(nil), p.requests...)
}

func TestRunner_TurnQualityInjectsCorrectionOnEmptyTurn(t *testing.T) {
	t.Parallel()
	reg := tools.NewRegistry()

	r := runner.New(
		runner.ClientFromProvider(&emptyTurnProvider{}),
		runner.WithTools(reg),
		runner.WithMaxIterations(5),
		runner.WithTurnQuality(runner.EmptyResponseDetector{}),
	)

	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "go",
	})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if res.Reason != runner.TerminalCompleted {
		t.Errorf("Reason = %q, want completed (after the second turn finally produces content)", res.Reason)
	}
	if !strings.Contains(res.FinalContent, "done now") {
		t.Errorf("FinalContent = %q, want 'done now' (iter 2's reply)", res.FinalContent)
	}
	// The corrective user message must be in the transcript so we
	// can see what nudge the model received.
	var sawCorrection bool
	for _, m := range res.Messages {
		if m.Role == "user" && strings.Contains(m.Content, "empty") {
			sawCorrection = true
			break
		}
	}
	if !sawCorrection {
		t.Error("expected an injected user-side correction message after the empty turn")
	}
}

func TestRunner_TurnQualityCanDisableThinkingForCorrection(t *testing.T) {
	t.Parallel()
	reg := tools.NewRegistry()
	provider := &recordingEmptyTurnProvider{}

	r := runner.New(
		runner.ClientFromProvider(provider),
		runner.WithTools(reg),
		runner.WithMaxIterations(5),
		runner.WithTurnQuality(runner.EmptyResponseDetector{
			DisableThinkingOnRetry: true,
			MaxCorrections:         1,
		}),
	)

	res := r.Run(t.Context(), runner.TaskSpec{
		ID:       taskscope.ID(uuid.NewString()),
		Prompt:   "go",
		Thinking: true,
	})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if res.Reason != runner.TerminalCompleted {
		t.Fatalf("Reason = %q, want completed", res.Reason)
	}
	reqs := provider.snapshotRequests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	if got := reqs[0].ChatTemplateKwargs["enable_thinking"]; got != true {
		t.Fatalf("request 1 enable_thinking = %v, want true", got)
	}
	if reqs[1].ChatTemplateKwargs != nil {
		t.Fatalf("request 2 ChatTemplateKwargs = %#v, want nil after thinking disabled", reqs[1].ChatTemplateKwargs)
	}
}

func TestRunner_NoTurnQualityExitsOnEmptyTurn(t *testing.T) {
	t.Parallel()
	// Sanity: without WithTurnQuality, the runner takes its
	// pre-C1 path — exit cleanly on the first empty turn. This
	// keeps the change opt-in for headless / non-zarlcode consumers
	// who don't want the loop to keep going on empty assistant turns.
	reg := tools.NewRegistry()

	r := runner.New(
		runner.ClientFromProvider(&emptyTurnProvider{}),
		runner.WithTools(reg),
		runner.WithMaxIterations(5),
	)

	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "go",
	})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if res.Iterations != 1 {
		t.Errorf("Iterations = %d, want 1 (no quality hook → first empty turn ends the loop)", res.Iterations)
	}
	if res.FinalContent != "" {
		t.Errorf("FinalContent = %q, want empty (no second iteration ran)", res.FinalContent)
	}
}
