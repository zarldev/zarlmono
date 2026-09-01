package runner_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type slowContentSink struct {
	runner.NopSink
	delay time.Duration
}

// OnContent intentionally delays the downstream sink; synctest makes the
// delay virtual while the runner proves downstream work does not trip idle timeouts.
func (s slowContentSink) OnContent(runner.Content) { time.Sleep(s.delay) }

type oneChunkProvider struct{}

func (oneChunkProvider) Name() string { return "one-chunk" }
func (oneChunkProvider) Complete(context.Context, llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		yield(llm.CompletionChunk{Content: "answer"}, nil)
	}
}

func TestRunner_StreamIdleExcludesDownstreamYield(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := runner.New(
			runner.ClientFromProvider(oneChunkProvider{}),
			runner.WithTools(tools.NewRegistry()),
			runner.WithSink(slowContentSink{delay: time.Second}),
			runner.WithMaxIterations(1),
			runner.WithStreamIdleTimeout(100*time.Millisecond),
		)
		res := r.Run(t.Context(), runner.TaskSpec{ID: taskscope.ID(uuid.NewString()), Prompt: "go"})
		if res.Err != nil {
			t.Fatalf("Run: %v", res.Err)
		}
		if res.FinalContent != "answer" {
			t.Fatalf("FinalContent = %q, want answer", res.FinalContent)
		}
	})
}

type acceptedThenRateLimited struct{ calls atomic.Int32 }

func (p *acceptedThenRateLimited) Name() string { return "accepted-then-rate-limited" }
func (p *acceptedThenRateLimited) Complete(context.Context, llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		p.calls.Add(1)
		if !yield(llm.CompletionChunk{}, nil) {
			return
		}
		yield(llm.CompletionChunk{}, &llm.RateLimitError{Message: "later", Retryable: true, RetryAfter: time.Nanosecond})
	}
}

func TestRunner_DoesNotRetryAfterAcceptedMetadataEvent(t *testing.T) {
	p := &acceptedThenRateLimited{}
	r := runner.New(
		runner.ClientFromProvider(p),
		runner.WithTools(tools.NewRegistry()),
		runner.WithMaxIterations(3),
	)
	res := r.Run(t.Context(), runner.TaskSpec{ID: taskscope.ID(uuid.NewString()), Prompt: "go"})
	if res.Reason != runner.TerminalError {
		t.Fatalf("Reason = %q, want error", res.Reason)
	}
	if got := p.calls.Load(); got != 1 {
		t.Fatalf("Complete invocations = %d, want 1", got)
	}
}

type callerCauseProvider struct{}

func (callerCauseProvider) Name() string { return "caller-cause" }
func (callerCauseProvider) Complete(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		<-ctx.Done()
	}
}

func TestRunner_PreservesCallerCancellationCause(t *testing.T) {
	callerCause := errors.New("caller stopped")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(callerCause)
	r := runner.New(
		runner.ClientFromProvider(callerCauseProvider{}),
		runner.WithTools(tools.NewRegistry()),
		runner.WithMaxIterations(1),
	)
	res := r.Run(ctx, runner.TaskSpec{ID: taskscope.ID(uuid.NewString()), Prompt: "go"})
	if !errors.Is(res.Err, callerCause) {
		t.Fatalf("Err = %v, want caller cause", res.Err)
	}
	if res.Cause != runner.TerminalCauseCaller {
		t.Fatalf("Cause = %q, want %q", res.Cause, runner.TerminalCauseCaller)
	}
}
