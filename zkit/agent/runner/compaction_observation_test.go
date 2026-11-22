package runner_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type auxiliarySink struct {
	runner.NopSink
	events      []runner.CompactionSettled
	ended       []runner.ConversationEnded
	applied     int
	mutate      bool
	appliedHook func()
}

func (s *auxiliarySink) OnCompactionSettled(_ context.Context, e runner.CompactionSettled) {
	s.events = append(s.events, e)
	if s.mutate {
		e.Inference.Usage.PromptTokens = 999
		e.Inference.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 999
		e.Inference.Execution.GatewayDeployment = "sink-mutation"
	}
}
func (s *auxiliarySink) OnConversationEnded(_ context.Context, e runner.ConversationEnded) {
	s.ended = append(s.ended, e)
}
func (s *auxiliarySink) OnCompactionApplied(context.Context, runner.CompactionApplied) {
	s.applied++
	if s.appliedHook != nil {
		s.appliedHook()
	}
}

func auxiliaryEvidence() compact.Inference {
	return compact.Inference{
		Provider: "auxiliary-provider", Model: "auxiliary-label", Usage: &llm.Usage{PromptTokens: 90},
		PricingUsage: &llm.PricingUsage{Input: llm.PricingInputUsage{WritesByLifetime: []llm.CacheWriteQuantity{{Quantity: llm.TokenQuantity{Known: true, Tokens: 5}}}}},
		Execution:    &llm.ExecutionObservation{GatewayDeployment: "auxiliary-deployment"},
	}
}

func TestCompactionObservationsAllTerminalResults(t *testing.T) {
	failure := errors.New("auxiliary failed")
	for _, tc := range []struct {
		name   string
		reason runner.TerminalReason
	}{
		{"completed", runner.TerminalCompleted},
		{"max-iterations", runner.TerminalMaxIterations},
		{"error", runner.TerminalError},
		{"cancelled", runner.TerminalCancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			observation := auxiliaryEvidence()
			original := observation.Clone()
			sink := &auxiliarySink{mutate: true}
			c := compact.Func(func(_ context.Context, history []llm.Message, _ int) (compact.Result, error) {
				result := compact.Result{History: history, Inference: &observation}
				switch tc.reason {
				case runner.TerminalError:
					return result, failure
				case runner.TerminalCancelled:
					cancel(failure)
					return result, context.Cause(ctx)
				default:
					return result, nil // No history change still settles evidence.
				}
			})
			second := []llm.CompletionChunk{{Content: "done", UsageReported: true, Usage: llm.Usage{PromptTokens: 4}}}
			if tc.reason == runner.TerminalMaxIterations {
				second = []llm.CompletionChunk{runnertest.ChunkToolCall("b", "echo", `{}`), {UsageReported: true, Usage: llm.Usage{PromptTokens: 4}}}
			}
			client := runnertest.NewClient([][]llm.CompletionChunk{
				{runnertest.ChunkToolCall("a", "echo", `{}`), {UsageReported: true, Usage: llm.Usage{PromptTokens: 10}}}, second,
			})
			result := runner.New(client, runner.WithSink(sink), runner.WithCompactor(c), runner.WithTools(tools.NewRegistry(runnertest.Tool{Name: "echo", Result: "ok"}))).Run(ctx, runner.TaskSpec{ID: "root", Prompt: "go", MaxIterations: 2})
			if result.Reason != tc.reason || len(result.CompactionObservations) != 1 || len(sink.events) != 1 || sink.applied != 0 || len(sink.ended) != 1 {
				t.Fatalf("result = %+v; sink = %+v", result, sink)
			}
			if !reflect.DeepEqual(result.CompactionObservations[0], original) || !reflect.DeepEqual(observation, original) {
				t.Fatalf("sink changed compactor/result evidence: %+v / %+v", observation, result.CompactionObservations)
			}
			if sink.events[0].TaskID != "root" || sink.events[0].Depth != 0 {
				t.Fatalf("settlement identity = %+v", sink.events[0])
			}
			wantTokens := 14
			if tc.reason == runner.TerminalError || tc.reason == runner.TerminalCancelled {
				wantTokens = 10
				if !errors.Is(result.Err, failure) || !errors.Is(result.Err, runner.ErrCompact) || !errors.Is(sink.events[0].Err, failure) {
					t.Fatalf("errors = %v / %v", result.Err, sink.events[0].Err)
				}
			}
			if result.TotalUsage == nil || result.TotalUsage.PromptTokens != wantTokens || sink.ended[0].TotalUsage == nil || sink.ended[0].TotalUsage.PromptTokens != wantTokens {
				t.Fatalf("auxiliary usage entered main totals: %+v / %+v", result.TotalUsage, sink.ended[0].TotalUsage)
			}
			observation.Usage.PromptTokens = 222
			observation.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 222
			observation.Execution.GatewayDeployment = "compactor-mutation"
			if !reflect.DeepEqual(result.CompactionObservations[0], original) {
				t.Fatal("result aliases compactor")
			}
			result.CompactionObservations[0].Usage.PromptTokens = 333
			result.CompactionObservations[0].PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 333
			result.CompactionObservations[0].Execution.GatewayDeployment = "result-mutation"
			if sink.events[0].Inference.Usage.PromptTokens != 999 || sink.events[0].Inference.Execution.GatewayDeployment != "sink-mutation" || observation.Usage.PromptTokens != 222 || result.LastUsage.PromptTokens == 333 {
				t.Fatal("terminal result aliases event, compactor or main usage")
			}
		})
	}
}

func TestCompactionObservationOrderAndRunIsolation(t *testing.T) {
	shared := auxiliaryEvidence()
	invocations := 0
	c := compact.Func(func(_ context.Context, history []llm.Message, _ int) (compact.Result, error) {
		invocations++
		shared.Usage.PromptTokens = invocations
		shared.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = int64(invocations)
		return compact.Result{History: history, Inference: &shared, BytesTrimmed: 1}, nil
	})
	client := runnertest.NewClient([][]llm.CompletionChunk{
		{runnertest.ChunkToolCall("a", "echo", `{}`)}, {runnertest.ChunkToolCall("b", "echo", `{}`)}, {{Content: "first done"}},
		{runnertest.ChunkToolCall("c", "echo", `{}`)}, {{Content: "second done"}},
	})
	sink := &auxiliarySink{}
	r := runner.New(client, runner.WithCompactor(c), runner.WithSink(sink), runner.WithTools(tools.NewRegistry(runnertest.Tool{Name: "echo", Result: "ok"})))
	first := r.Run(t.Context(), runner.TaskSpec{ID: "first", Prompt: "go"})
	second := r.Run(t.Context(), runner.TaskSpec{ID: "second", Prompt: "again", Context: first.Messages})
	if first.Err != nil || second.Err != nil || len(first.CompactionObservations) != 2 || len(second.CompactionObservations) != 1 || sink.applied != 3 || len(sink.events) != 3 {
		t.Fatalf("first = %+v; second = %+v; sink = %+v", first, second, sink)
	}
	for i, o := range append(first.CompactionObservations[:len(first.CompactionObservations):len(first.CompactionObservations)], second.CompactionObservations...) {
		if o.Usage.PromptTokens != i+1 || o.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens != int64(i+1) {
			t.Fatalf("settlement %d was overwritten: %+v", i, o)
		}
	}
	first.CompactionObservations[0].Usage.PromptTokens = 999
	if first.CompactionObservations[1].Usage.PromptTokens != 2 || second.CompactionObservations[0].Usage.PromptTokens != 3 || sink.events[0].Inference.Usage.PromptTokens != 1 || shared.Usage.PromptTokens != 3 {
		t.Fatal("observations alias each other, another Run, sink or compactor")
	}
}

type refusedCompactionProvider struct{ calls int }

func (*refusedCompactionProvider) Name() string { return "refused-auxiliary" }
func (p *refusedCompactionProvider) Complete(context.Context, llm.CompletionRequest) llm.CompletionStream {
	p.calls++
	return func(yield func(llm.CompletionChunk, error) bool) { yield(llm.CompletionChunk{Content: "summary"}, nil) }
}

func TestCompactionObservationsAbsentWhenNotInvoked(t *testing.T) {
	for _, mode := range []string{"unconfigured", "iteration-zero", "prober-skip", "model-free", "admission-refused"} {
		t.Run(mode, func(t *testing.T) {
			sink := &auxiliarySink{}
			client := runnertest.NewClient([][]llm.CompletionChunk{{runnertest.ChunkToolCall("a", "echo", `{}`)}, {{Content: "done"}}})
			var c compact.Compactor
			p := &refusedCompactionProvider{}
			switch mode {
			case "iteration-zero":
				c = compact.Func(func(context.Context, []llm.Message, int) (compact.Result, error) {
					t.Fatal("compaction ran at iteration zero")
					return compact.Result{}, nil
				})
				client = runnertest.NewClient([][]llm.CompletionChunk{{{Content: "done"}}})
			case "prober-skip":
				c = compact.NewSummary(p, "auxiliary") // Keep window contains the entire short history.
			case "model-free":
				c = compact.NewStructural()
			case "admission-refused":
				account := inference.New(inference.WithLimits(inference.Limits{Invocations: 1}))
				scope := inference.Scope{Account: account}
				for range scope.Wrap(t.Context(), inference.Call{}, func(func(llm.CompletionChunk, error) bool) {}) {
				}
				summary := compact.NewSummary(p, "auxiliary")
				summary.InferenceScope = scope
				c = summary
			}
			r := runner.New(client, runner.WithSink(sink), runner.WithCompactor(c), runner.WithTools(tools.NewRegistry(runnertest.Tool{Name: "echo", Result: "ok"})))
			spec := runner.TaskSpec{Prompt: "go"}
			if mode == "model-free" || mode == "admission-refused" {
				// Longer older input makes the compactor eligible despite exchange retention.
				for range 30 {
					spec.Context = append(spec.Context, llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("old", compact.AssistantContentTrimAt)})
				}
			}
			result := r.Run(t.Context(), spec)
			if result.CompactionObservations != nil || len(sink.events) != 0 || p.calls != 0 {
				t.Fatalf("fabricated compaction record: %+v / %+v; calls %d", result, sink.events, p.calls)
			}
			if mode == "model-free" && sink.applied != 1 {
				t.Fatal("model-free compaction did no work")
			}
			if mode == "admission-refused" {
				if !errors.Is(result.Err, runner.ErrCompact) || !errors.Is(result.Err, inference.ErrBudget) {
					t.Fatalf("admission error = %v", result.Err)
				}
			} else if result.Err != nil {
				t.Fatal(result.Err)
			}
		})
	}
}

func TestCompactionObservationsExcludeDescendants(t *testing.T) {
	var r *runner.Runner
	var child runner.TaskResult
	c := compact.Func(func(ctx context.Context, history []llm.Message, _ int) (compact.Result, error) {
		if taskscope.DepthFrom(ctx) == 0 {
			child = r.Run(ctx, runner.TaskSpec{ID: "child", Depth: 1, Prompt: "child"})
		}
		o := auxiliaryEvidence()
		o.Model = string(taskscope.IDFrom(ctx))
		return compact.Result{History: history, Inference: &o}, nil
	})
	client := observationClient(func(_ context.Context, req llm.CompletionRequest) llm.CompletionStream {
		return func(yield func(llm.CompletionChunk, error) bool) {
			last := req.Messages[len(req.Messages)-1]
			if last.Role == llm.RoleUser && (last.Content == "root" || last.Content == "child") {
				chunk := runnertest.ChunkToolCall("echo", "echo", `{}`)
				chunk.UsageReported, chunk.Usage.PromptTokens = true, 3
				yield(chunk, nil)
				return
			}
			yield(llm.CompletionChunk{Content: "answer", UsageReported: true, Usage: llm.Usage{PromptTokens: 7}}, nil)
		}
	})
	sink := &auxiliarySink{}
	r = runner.New(client, runner.WithCompactor(c), runner.WithSink(sink), runner.WithTools(tools.NewRegistry(runnertest.Tool{Name: "echo", Result: "ok"})))
	root := r.Run(t.Context(), runner.TaskSpec{ID: "root", Prompt: "root"})
	next := r.Run(t.Context(), runner.TaskSpec{ID: "root", Prompt: "next", Context: root.Messages})
	if root.Err != nil || child.Err != nil || next.Err != nil || len(root.CompactionObservations) != 1 || len(child.CompactionObservations) != 1 || next.CompactionObservations != nil {
		t.Fatalf("root = %+v; child = %+v; next = %+v", root, child, next)
	}
	if root.CompactionObservations[0].Model != "root" || child.CompactionObservations[0].Model != "child" || len(sink.events) != 2 || sink.events[0].TaskID != "child" || sink.events[0].Depth != 1 || sink.events[1].TaskID != "root" || sink.events[1].Depth != 0 {
		t.Fatalf("cross-task evidence: root = %+v; child = %+v; events = %+v", root.CompactionObservations, child.CompactionObservations, sink.events)
	}
	if root.TotalUsage.PromptTokens != 10 || child.TotalUsage.PromptTokens != 10 || len(root.ProviderObservations) != 2 || len(child.ProviderObservations) != 2 || root.Timing.ProviderAttempts != 2 {
		t.Fatal("descendant or auxiliary usage/attempts entered root totals")
	}
	child.CompactionObservations[0].PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 999
	if root.CompactionObservations[0].PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens != 5 || sink.events[0].Inference.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens != 5 {
		t.Fatal("child result aliases root or sink")
	}
}

func TestCompactionObservationsConcurrentRuns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reached := make(chan struct{}, 2)
		release := make(chan struct{})
		c := compact.Func(func(ctx context.Context, history []llm.Message, _ int) (compact.Result, error) {
			reached <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return compact.Result{}, context.Cause(ctx)
			}
			o := auxiliaryEvidence()
			o.Model = string(taskscope.IDFrom(ctx))
			return compact.Result{History: history, Inference: &o}, nil
		})
		client := observationClient(func(_ context.Context, req llm.CompletionRequest) llm.CompletionStream {
			return func(yield func(llm.CompletionChunk, error) bool) {
				if req.Messages[len(req.Messages)-1].Role == llm.RoleUser {
					yield(runnertest.ChunkToolCall("echo", "echo", `{}`), nil)
					return
				}
				yield(llm.CompletionChunk{Content: "done"}, nil)
			}
		})
		sink := &auxiliarySink{}
		r := runner.New(client, runner.WithCompactor(c), runner.WithSink(runner.NewSyncSink(sink)), runner.WithTools(tools.NewRegistry(runnertest.Tool{Name: "echo", Result: "ok"})))
		results := make([]runner.TaskResult, 2)
		var wg sync.WaitGroup
		for i := range results {
			wg.Go(func() {
				id := taskscope.ID([]string{"one", "two"}[i])
				results[i] = r.Run(t.Context(), runner.TaskSpec{ID: id, Prompt: "go"})
			})
		}
		<-reached
		<-reached
		close(release)
		wg.Wait()
		for _, result := range results {
			if result.Err != nil || len(result.CompactionObservations) != 1 || result.CompactionObservations[0].Model != string(result.ID) {
				t.Fatalf("concurrent result = %+v", result)
			}
		}
		if len(sink.events) != 2 {
			t.Fatalf("settlements = %+v", sink.events)
		}
		results[0].CompactionObservations[0].Usage.PromptTokens = 999
		if results[1].CompactionObservations[0].Usage.PromptTokens != 90 {
			t.Fatal("concurrent Run snapshots alias")
		}
	})
}

func TestCompactionObservationOwnedBeforeAppliedCallback(t *testing.T) {
	observation := auxiliaryEvidence()
	original := observation.Clone()
	sink := &auxiliarySink{}
	sink.appliedHook = func() {
		if len(sink.events) != 1 {
			t.Fatal("compaction applied before inference settled")
		}
		observation.Usage.PromptTokens = 999
		observation.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 999
		observation.Execution.GatewayDeployment = "applied-callback-mutation"
	}
	c := compact.Func(func(_ context.Context, history []llm.Message, _ int) (compact.Result, error) {
		return compact.Result{History: history, Inference: &observation, BytesTrimmed: 1}, nil
	})
	client := runnertest.NewClient([][]llm.CompletionChunk{{runnertest.ChunkToolCall("echo", "echo", `{}`)}, {{Content: "done"}}})
	result := runner.New(client, runner.WithCompactor(c), runner.WithSink(sink), runner.WithTools(tools.NewRegistry(runnertest.Tool{Name: "echo", Result: "ok"}))).Run(t.Context(), runner.TaskSpec{Prompt: "go"})
	if result.Err != nil || len(result.CompactionObservations) != 1 || len(sink.events) != 1 || sink.applied != 1 || !reflect.DeepEqual(result.CompactionObservations[0], original) || !reflect.DeepEqual(sink.events[0].Inference, original) {
		t.Fatalf("borrowed compactor evidence escaped publication: result = %+v; events = %+v", result, sink.events)
	}
}

func TestCompactionObservationRetainedAfterLaterProviderFailure(t *testing.T) {
	failure := errors.New("main model stopped")
	for _, cancelRun := range []bool{false, true} {
		name := "error"
		if cancelRun {
			name = "cancelled"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			c := compact.Func(func(_ context.Context, history []llm.Message, _ int) (compact.Result, error) {
				o := auxiliaryEvidence()
				return compact.Result{History: history, Inference: &o}, nil
			})
			calls := 0
			client := observationClient(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
				return func(yield func(llm.CompletionChunk, error) bool) {
					calls++
					if calls == 1 {
						yield(runnertest.ChunkToolCall("echo", "echo", `{}`), nil)
						return
					}
					if cancelRun {
						cancel(failure)
					}
					yield(llm.CompletionChunk{}, failure)
				}
			})
			result := runner.New(client, runner.WithCompactor(c), runner.WithTools(tools.NewRegistry(runnertest.Tool{Name: "echo", Result: "ok"}))).Run(ctx, runner.TaskSpec{Prompt: "go"})
			want := runner.TerminalError
			if cancelRun {
				want = runner.TerminalCancelled
			}
			if result.Reason != want || !errors.Is(result.Err, failure) || len(result.CompactionObservations) != 1 || !reflect.DeepEqual(result.CompactionObservations[0], auxiliaryEvidence()) || result.TotalUsage != nil {
				t.Fatalf("terminal lost earlier compaction: %+v", result)
			}
		})
	}
}
