package runner_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

type usageAttemptClient struct {
	calls    int
	usages   []*llm.Usage
	failures []error
}

func (c *usageAttemptClient) Complete(context.Context, llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		i := c.calls
		c.calls++
		if i >= len(c.usages) {
			yield(llm.CompletionChunk{}, errors.New("unexpected attempt"))
			return
		}
		if c.usages[i] != nil {
			// Providers may report cumulative snapshots repeatedly; only the last is billed.
			if !yield(llm.CompletionChunk{Usage: llm.Usage{PromptTokens: 1}, UsageReported: true}, nil) {
				return
			}
			if !yield(llm.CompletionChunk{Usage: *c.usages[i], UsageReported: true}, nil) {
				return
			}
		}
		if c.failures[i] != nil {
			yield(llm.CompletionChunk{}, c.failures[i])
			return
		}
		yield(runnertest.ChunkText("answer"), nil)
	}
}

type usageAttemptSink struct {
	runner.NopSink
	attempts []runner.ProviderAttemptSettled
	terminal runner.ConversationEnded
}

func (s *usageAttemptSink) OnProviderAttemptSettled(ctx context.Context, e runner.ProviderAttemptSettled) {
	s.attempts = append(s.attempts, e)
}
func (s *usageAttemptSink) OnConversationEnded(ctx context.Context, e runner.ConversationEnded) {
	s.terminal = e
}

func TestAllAttemptUsageAndObservability(t *testing.T) {
	reported := &llm.Usage{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14, CachedTokens: 3}
	failure := errors.New("provider terminal")
	retry := errors.New("Failed to parse tool call arguments as JSON: bad escape")
	for _, tc := range []struct {
		name     string
		usages   []*llm.Usage
		failures []error
		total    *llm.Usage
		cause    error
	}{
		{"success", []*llm.Usage{reported}, []error{nil}, reported, nil},
		{"error", []*llm.Usage{reported}, []error{failure}, reported, failure},
		{"cancelled", []*llm.Usage{reported}, []error{context.Canceled}, reported, context.Canceled},
		{"deadline", []*llm.Usage{reported}, []error{context.DeadlineExceeded}, reported, context.DeadlineExceeded},
		{"unreported", []*llm.Usage{nil}, []error{nil}, nil, nil},
		{"reported-zero", []*llm.Usage{{}}, []error{nil}, &llm.Usage{}, nil},
		{"retry", []*llm.Usage{reported, reported}, []error{retry, nil}, &llm.Usage{PromptTokens: 20, CompletionTokens: 8, TotalTokens: 28, CachedTokens: 6}, nil},
		{"retry-unreported", []*llm.Usage{reported, nil}, []error{retry, nil}, reported, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &usageAttemptClient{usages: tc.usages, failures: tc.failures}
			sink := &usageAttemptSink{}
			result := runner.New(client, runner.WithSink(sink)).Run(t.Context(), runner.TaskSpec{ID: "task", Prompt: "question"})
			if !errors.Is(result.Err, tc.cause) {
				t.Fatalf("terminal error = %v; want %v", result.Err, tc.cause)
			}
			if !reflect.DeepEqual(result.TotalUsage, tc.total) || !reflect.DeepEqual(sink.terminal.TotalUsage, tc.total) {
				t.Fatalf("total result/event = %+v / %+v; want %+v", result.TotalUsage, sink.terminal.TotalUsage, tc.total)
			}
			if client.calls != len(tc.usages) || len(sink.attempts) != client.calls {
				t.Fatalf("calls/events = %d/%d", client.calls, len(sink.attempts))
			}
			for i, event := range sink.attempts {
				if event.Attempt != i+1 || event.TaskID != "task" || !reflect.DeepEqual(event.Usage, tc.usages[i]) || !errors.Is(event.Err, tc.failures[i]) {
					t.Fatalf("attempt %d = %+v", i, event)
				}
			}
			if sink.attempts[0].Usage != nil {
				sink.attempts[0].Usage.PromptTokens = 999
				if result.TotalUsage.PromptTokens == 999 || reported.PromptTokens == 999 {
					t.Fatal("attempt snapshot aliases usage authority")
				}
			}
		})
	}
}

type rejectPreparedHistory struct{ cause error }

func (s rejectPreparedHistory) Append(context.Context, []runner.ReplayMessage) error { return nil }
func (s rejectPreparedHistory) Request(context.Context, llm.CompletionRequest) error { return s.cause }

func TestPreparationFailureConsumesNoAttempt(t *testing.T) {
	cause := errors.New("prepared request rejected")
	client, sink := &usageAttemptClient{}, &usageAttemptSink{}
	result := runner.New(client, runner.WithSink(sink), runner.WithHistorySink(rejectPreparedHistory{cause})).Run(t.Context(), runner.TaskSpec{ID: "task", Prompt: "question"})
	if !errors.Is(result.Err, runner.ErrReplayHistory) || !errors.Is(result.Err, cause) {
		t.Fatalf("result = %v", result.Err)
	}
	if client.calls != 0 || len(sink.attempts) != 0 || result.TotalUsage != nil {
		t.Fatal("preparation invented an attempt or usage")
	}
}
