package runner_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type timingClient func(context.Context, llm.CompletionRequest) llm.CompletionStream

func (f timingClient) Complete(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
	return f(ctx, req)
}

type timingSink struct {
	runner.NopSink
	attempts   []runner.ProviderAttemptSettled
	iterations []runner.IterationCompleted
	delay      time.Duration
}

func (s *timingSink) OnProviderAttemptSettled(_ context.Context, e runner.ProviderAttemptSettled) {
	s.attempts = append(s.attempts, e)
}
func (s *timingSink) OnIterationCompleted(_ context.Context, e runner.IterationCompleted) {
	s.iterations = append(s.iterations, e)
}
func (s *timingSink) OnContent(context.Context, runner.Content) { time.Sleep(s.delay) }

func TestAttemptTimingSeparatesOutputAndSynchronousHandling(t *testing.T) {
	for _, tc := range []struct {
		name     string
		chunk    llm.CompletionChunk
		output   bool
		accepted bool
		callback time.Duration
	}{
		{"content", runnertest.ChunkText("answer"), true, true, 3 * time.Second},
		{"thinking", llm.CompletionChunk{Thinking: "thought"}, true, true, 0},
		{"tool", runnertest.ChunkToolCall("id", "noop", "{}"), true, true, 0},
		{"usage only", llm.CompletionChunk{UsageReported: true}, false, false, 0},
		{"finish", llm.CompletionChunk{FinishReason: llm.FinishReasons.STOP}, false, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				sink := &timingSink{delay: 3 * time.Second}
				client := timingClient(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
					// Construction is deliberately outside the measured lazy invocation.
					time.Sleep(7 * time.Second)
					return func(yield func(llm.CompletionChunk, error) bool) {
						time.Sleep(time.Second)
						if !yield(llm.CompletionChunk{UsageReported: true}, nil) {
							return
						}
						time.Sleep(time.Second)
						if !yield(tc.chunk, nil) {
							return
						}
						time.Sleep(time.Second)
					}
				})
				res := runner.New(client, runner.WithSink(sink)).Run(t.Context(), runner.TaskSpec{ID: "timing", Prompt: "test", MaxIterations: 1})
				if len(sink.attempts) != 1 {
					t.Fatalf("attempts=%d", len(sink.attempts))
				}
				a := sink.attempts[0]
				if a.Duration != 3*time.Second+tc.callback || a.CallbackDuration != tc.callback {
					t.Fatalf("attempt timing=%+v", a)
				}
				if (a.TimeToFirstOutput != nil) != tc.output || (a.TimeToFirstAccepted != nil) != tc.accepted {
					t.Fatalf("first output/accepted=%+v", a)
				}
				if tc.output && *a.TimeToFirstOutput != 2*time.Second {
					t.Fatalf("first output=%s", *a.TimeToFirstOutput)
				}
				if tc.accepted && *a.TimeToFirstAccepted != 2*time.Second {
					t.Fatalf("first accepted=%s", *a.TimeToFirstAccepted)
				}
				if res.Timing.ProviderAttempts != 1 || res.Timing.AttemptsWithUsage != 1 || res.Timing.ProviderDuration != a.Duration {
					t.Fatalf("totals=%+v", res.Timing)
				}
				if (res.Timing.TimeToFirstOutput != nil) != tc.output {
					t.Fatalf("task first output=%v", res.Timing.TimeToFirstOutput)
				}
				if tc.output && *res.Timing.TimeToFirstOutput != 9*time.Second {
					t.Fatalf("task first output=%s", *res.Timing.TimeToFirstOutput)
				}
				residual := res.Duration - res.Timing.ProviderDuration - res.Timing.RequestPreparationDuration - res.Timing.ToolDispatchDuration
				if residual != 7*time.Second {
					t.Fatalf("construction residual=%s", residual)
				}
			})
		})
	}
}

func TestAttemptTimingErrorRetryAndCancellation(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "retry", true: "cancel"}[cancel], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, stop := context.WithCancel(t.Context())
				defer stop()
				sink := &timingSink{}
				attempt := 0
				client := timingClient(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
					return func(yield func(llm.CompletionChunk, error) bool) {
						attempt++
						time.Sleep(2 * time.Second)
						if cancel {
							stop()
							yield(llm.CompletionChunk{}, context.Canceled)
							return
						}
						if attempt == 1 {
							yield(llm.CompletionChunk{}, &llm.RateLimitError{Retryable: true, RetryAfter: time.Second})
							return
						}
						yield(runnertest.ChunkText("done"), nil)
					}
				})
				res := runner.New(client, runner.WithSink(sink)).Run(ctx, runner.TaskSpec{Prompt: "test"})
				if cancel {
					if !errors.Is(res.Err, context.Canceled) || len(sink.attempts) != 1 {
						t.Fatalf("result=%+v attempts=%+v", res, sink.attempts)
					}
				} else if res.Err != nil || len(sink.attempts) != 2 {
					t.Fatalf("result=%+v attempts=%+v", res, sink.attempts)
				}
				for _, a := range sink.attempts {
					if a.Duration != 2*time.Second {
						t.Fatalf("duration=%s", a.Duration)
					}
				}
				if sink.attempts[0].TimeToFirstOutput != nil || res.Timing.AttemptsWithUsage != 0 {
					t.Fatalf("unknown output/usage=%+v", res.Timing)
				}
				if res.Timing.ProviderDuration != time.Duration(len(sink.attempts))*2*time.Second {
					t.Fatalf("totals=%+v", res.Timing)
				}
				residual := res.Duration - res.Timing.ProviderDuration - res.Timing.RequestPreparationDuration - res.Timing.ToolDispatchDuration
				if cancel {
					if residual != 0 || res.Timing.TimeToFirstOutput != nil {
						t.Fatalf("cancel timing=%+v residual=%s", res.Timing, residual)
					}
				} else if residual != time.Second || res.Timing.TimeToFirstOutput == nil || *res.Timing.TimeToFirstOutput != 5*time.Second {
					t.Fatalf("retry timing=%+v residual=%s", res.Timing, residual)
				}
			})
		})
	}
}

func TestIterationTimingMeasuresPreparationAndBatchWallTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink := &timingSink{}
		read := tools.New(tools.ToolSpec{Name: "read", WorkspaceAccess: tools.WorkspaceAccesses.READ}, func(context.Context, map[string]any) (string, error) { time.Sleep(time.Second); return "contents", nil })
		prompt := runner.PromptFunc(func(context.Context, runner.PromptVars) (string, error) {
			time.Sleep(2 * time.Second)
			return "system", nil
		})
		res := runner.New(runnertest.NewClient(readBatchChunks(5)), runner.WithSink(sink), runner.WithTools(tools.NewRegistry(read)), runner.WithPrompt(prompt)).Run(t.Context(), runner.TaskSpec{Prompt: "test"})
		if res.Err != nil {
			t.Fatal(res.Err)
		}
		if len(sink.iterations) != 2 {
			t.Fatalf("iterations=%d", len(sink.iterations))
		}
		first := sink.iterations[0]
		if first.RequestPreparationDuration != 2*time.Second || first.ToolDispatchDuration != 2*time.Second || sink.iterations[1].ToolDispatchDuration != 0 {
			t.Fatalf("iteration timing=%+v", sink.iterations)
		}
		if res.Timing.ToolDispatchDuration != 2*time.Second || res.Timing.RequestPreparationDuration != 2*time.Second || res.Duration != 4*time.Second {
			t.Fatalf("totals=%+v duration=%s", res.Timing, res.Duration)
		}
	})
}
