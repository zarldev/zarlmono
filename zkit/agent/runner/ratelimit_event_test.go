package runner_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// A terminal *llm.RateLimitError must reach ConversationEnded.RateLimit as
// the typed value (not just flattened into Error), so subscribers can render
// structured timing without re-parsing the message.
func TestRun_RateLimitErrorPopulatesConversationEnded(t *testing.T) {
	t.Parallel()

	rle := &llm.RateLimitError{Message: "slow down", RetryAfter: 30 * time.Second}
	provider := &fakeProvider{
		turns: [][]llm.CompletionChunk{
			{{Error: rle}},
		},
	}
	sink := newRecordingSink()

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	t.Cleanup(cancel)

	r := runner.New(
		runner.ClientFromProvider(provider),
		runner.WithTools(newRegistry()),
		runner.WithSink(sink),
		runner.WithMaxIterations(3),
	)
	res := r.Run(ctx, runner.TaskSpec{ID: taskscope.ID(uuid.NewString()), Prompt: "ping"})
	if res.Reason != runner.TerminalError {
		t.Fatalf("Reason = %v, want TerminalError", res.Reason)
	}

	sink.mu.Lock()
	ended := append([]runner.ConversationEnded(nil), sink.convEnded...)
	sink.mu.Unlock()
	if len(ended) != 1 {
		t.Fatalf("ConversationEnded count = %d, want 1", len(ended))
	}
	got := ended[0]
	if got.RateLimit == nil {
		t.Fatal("ConversationEnded.RateLimit = nil, want the typed rate-limit error")
	}
	if got.RateLimit.RetryAfter != 30*time.Second {
		t.Errorf("RateLimit.RetryAfter = %v, want 30s", got.RateLimit.RetryAfter)
	}
	if got.Error == "" {
		t.Error("Error string should still be populated alongside RateLimit")
	}
}

// A non-rate-limit terminal error leaves RateLimit nil.
func TestRun_NonRateLimitErrorLeavesRateLimitNil(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		turns: [][]llm.CompletionChunk{
			{{Error: context.DeadlineExceeded}},
		},
	}
	sink := newRecordingSink()

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	t.Cleanup(cancel)

	r := runner.New(
		runner.ClientFromProvider(provider),
		runner.WithTools(newRegistry()),
		runner.WithSink(sink),
		runner.WithMaxIterations(3),
	)
	_ = r.Run(ctx, runner.TaskSpec{ID: taskscope.ID(uuid.NewString()), Prompt: "ping"})

	sink.mu.Lock()
	ended := append([]runner.ConversationEnded(nil), sink.convEnded...)
	sink.mu.Unlock()
	for _, e := range ended {
		if e.RateLimit != nil {
			t.Errorf("RateLimit = %v, want nil for non-rate-limit error", e.RateLimit)
		}
	}
}

func TestRun_RateLimitRetryBudgetResetsAfterSuccess(t *testing.T) {
	t.Parallel()

	rateLimited := func() llm.CompletionChunk {
		return chunkErr(&llm.RateLimitError{
			Message:    "slow down",
			RetryAfter: time.Nanosecond,
			Retryable:  true,
		})
	}
	provider := &fakeProvider{turns: [][]llm.CompletionChunk{
		{rateLimited()},
		{chunkToolCall("call-1", "echo", `{}`), chunkDone()},
		{rateLimited()},
		{rateLimited()},
		{rateLimited()},
		{chunkText("finished"), chunkDone()},
	}}
	r := runner.New(
		runner.ClientFromProvider(provider),
		runner.WithTools(newRegistry(stubTool{name: "echo", result: "ok"})),
		runner.WithMaxIterations(8),
	)

	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "use echo, then finish",
	})
	if res.Err != nil {
		t.Fatalf("Run returned error: %v", res.Err)
	}
	if res.FinalContent != "finished" {
		t.Fatalf("FinalContent = %q, want %q", res.FinalContent, "finished")
	}
	if got := provider.callCount(); got != 6 {
		t.Fatalf("provider calls = %d, want 6", got)
	}
}
