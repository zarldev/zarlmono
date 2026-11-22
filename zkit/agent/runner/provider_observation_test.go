package runner_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type observationClient func(context.Context, llm.CompletionRequest) llm.CompletionStream

func (c observationClient) Complete(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
	return c(ctx, req)
}

type observationSink struct {
	runner.NopSink
	events []runner.ProviderAttemptSettled
	mutate bool
}

func (s *observationSink) OnProviderAttemptSettled(_ context.Context, e runner.ProviderAttemptSettled) {
	s.events = append(s.events, e)
	if s.mutate {
		if e.Usage != nil {
			e.Usage.PromptTokens = 999
		}
		if e.PricingUsage != nil {
			e.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 999
		}
		if e.Execution != nil {
			e.Execution.GatewayDeployment = "sink-mutation"
		}
	}
}

func TestProviderObservationPresence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		chunk llm.CompletionChunk
	}{
		{name: "absent"},
		{name: "legacy-does-not-imply-sidecars", chunk: llm.CompletionChunk{UsageReported: true}},
		{name: "present-unknown", chunk: llm.CompletionChunk{PricingUsage: &llm.PricingUsage{}, Execution: &llm.ExecutionObservation{}}},
		{name: "known-zero", chunk: llm.CompletionChunk{
			// Normalized known-zero pricing must be supplied explicitly by the provider.
			PricingUsage: &llm.PricingUsage{
				Input: llm.PricingInputUsage{
					Total:            llm.TokenQuantity{Known: true},
					WritesByLifetime: []llm.CacheWriteQuantity{{Lifetime: 5 * time.Minute}, {Lifetime: time.Hour}},
				},
				Output:                    llm.PricingOutputUsage{Total: llm.TokenQuantity{Known: true}},
				AdditionalChargesPossible: true,
			},
			Execution: &llm.ExecutionObservation{RetryCount: llm.ObservedCount{Known: true}, ReportedCost: llm.ReportedCost{Reported: true}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &observationSink{}
			client := runnertest.NewClient([][]llm.CompletionChunk{{tc.chunk, {Content: "answer"}}})
			result := runner.New(client, runner.WithSink(sink)).Run(t.Context(), runner.TaskSpec{Prompt: "go"})
			if result.Err != nil || len(result.ProviderObservations) != 1 || len(sink.events) != 1 {
				t.Fatalf("result = %+v, events = %+v", result, sink.events)
			}
			var usage *llm.Usage
			if tc.chunk.UsageReported {
				usage = &tc.chunk.Usage
			}
			want := runner.ProviderAttemptObservation{Attempt: 1, Usage: usage, PricingUsage: tc.chunk.PricingUsage, Execution: tc.chunk.Execution}
			if !reflect.DeepEqual(result.ProviderObservations[0], want) ||
				!reflect.DeepEqual(sink.events[0].PricingUsage, want.PricingUsage) || !reflect.DeepEqual(sink.events[0].Execution, want.Execution) {
				t.Fatalf("observation = %+v, event = %+v; want %+v", result.ProviderObservations[0], sink.events[0], want)
			}
		})
	}
}

func TestProviderObservationLatestIndependentSnapshotsAndOwnership(t *testing.T) {
	failure := errors.New("provider terminal")
	pricing := llm.PricingUsage{Input: llm.PricingInputUsage{WritesByLifetime: []llm.CacheWriteQuantity{{Quantity: llm.TokenQuantity{Tokens: 2, Known: true}}}}}
	execution := llm.ExecutionObservation{GatewayDeployment: "early-deployment"}
	client := observationClient(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
		return func(yield func(llm.CompletionChunk, error) bool) {
			if !yield(llm.CompletionChunk{PricingUsage: &pricing, Execution: &execution}, nil) {
				return
			}
			pricing.Input.WritesByLifetime[0].Quantity.Tokens = 5
			if !yield(llm.CompletionChunk{PricingUsage: &pricing, UsageReported: true, Usage: llm.Usage{PromptTokens: 3}}, nil) {
				return
			}
			// Providers may reuse borrowed data immediately after each yield.
			pricing.Input.WritesByLifetime[0].Quantity.Tokens = 100
			execution.GatewayDeployment = "provider-mutation"
			if !yield(llm.CompletionChunk{UsageReported: true, Usage: llm.Usage{PromptTokens: 7}}, nil) {
				return
			}
			yield(llm.CompletionChunk{}, failure)
		}
	})
	sink := &observationSink{mutate: true}
	result := runner.New(client, runner.WithSink(sink)).Run(t.Context(), runner.TaskSpec{ID: "owned", Prompt: "go"})
	if !errors.Is(result.Err, failure) || len(result.ProviderObservations) != 1 {
		t.Fatalf("result = %+v", result)
	}
	got := result.ProviderObservations[0]
	if got.Usage.PromptTokens != 7 || got.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens != 5 || got.Execution.GatewayDeployment != "early-deployment" {
		t.Fatalf("latest owned snapshots = %+v / %+v / %+v", got.Usage, got.PricingUsage, got.Execution)
	}
	if result.TotalUsage.PromptTokens != 7 || result.LastUsage.PromptTokens != 7 || pricing.Input.WritesByLifetime[0].Quantity.Tokens != 100 {
		t.Fatal("sink changed legacy totals or provider storage")
	}
	if sink.events[0].TimeToFirstOutput != nil || sink.events[0].TimeToFirstAccepted != nil {
		t.Fatal("sidecar-only evidence counted as output")
	}
	clone := got.Clone()
	clone.Usage.PromptTokens = 200
	clone.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 200
	clone.Execution.GatewayDeployment = "clone-mutation"
	if reflect.DeepEqual(got, clone) || got.Usage.PromptTokens != 7 || got.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens != 5 || got.Execution.GatewayDeployment != "early-deployment" {
		t.Fatal("Clone aliases original observation")
	}
	got.Usage.PromptTokens = 300
	got.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 300
	got.Execution.GatewayDeployment = "result-mutation"
	if sink.events[0].Usage.PromptTokens != 999 || sink.events[0].Execution.GatewayDeployment != "sink-mutation" || result.LastUsage.PromptTokens != 7 {
		t.Fatal("result aliases event or legacy occupancy")
	}
}

func TestProviderObservationRecoveryAcceptance(t *testing.T) {
	retry := errors.New("Failed to parse tool call arguments as JSON: bad escape")
	for _, tc := range []struct {
		name     string
		chunk    llm.CompletionChunk
		accepted bool
		output   bool
	}{
		{name: "pricing-only", chunk: llm.CompletionChunk{PricingUsage: &llm.PricingUsage{}}},
		{name: "execution-only", chunk: llm.CompletionChunk{Execution: &llm.ExecutionObservation{GatewayDeployment: "failed-deployment"}}},
		{name: "sidecars-and-usage", chunk: llm.CompletionChunk{PricingUsage: &llm.PricingUsage{}, Execution: &llm.ExecutionObservation{}, UsageReported: true}},
		{name: "ordinary-zero", accepted: true},
		{name: "content", chunk: llm.CompletionChunk{Content: "partial", Execution: &llm.ExecutionObservation{}}, accepted: true, output: true},
		{name: "thinking", chunk: llm.CompletionChunk{Thinking: "reason", PricingUsage: &llm.PricingUsage{}}, accepted: true, output: true},
		{name: "tool", chunk: runnertest.ChunkToolCall("c", "echo", `{}`), accepted: true, output: true},
		{name: "native", chunk: llm.CompletionChunk{CompletedItems: []llm.ContinuationItem{{Provider: "p", Format: "f", Data: []byte("native")}}}, accepted: true},
		{name: "finish", chunk: llm.CompletionChunk{FinishReason: llm.FinishReasons.STOP}, accepted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := observationClient(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
				return func(yield func(llm.CompletionChunk, error) bool) {
					calls++
					if calls == 1 {
						if yield(tc.chunk, nil) {
							yield(llm.CompletionChunk{}, retry)
						}
						return
					}
					yield(llm.CompletionChunk{Content: "answer", Execution: &llm.ExecutionObservation{GatewayDeployment: "successful-deployment"}}, nil)
				}
			})
			sink := &observationSink{}
			result := runner.New(client, runner.WithSink(sink)).Run(t.Context(), runner.TaskSpec{ID: "task", Prompt: "go"})
			wantAttempts := 2
			var wantErr error
			if tc.accepted {
				wantAttempts, wantErr = 1, retry
			}
			if !errors.Is(result.Err, wantErr) || calls != wantAttempts || len(result.ProviderObservations) != wantAttempts || len(sink.events) != wantAttempts {
				t.Fatalf("calls = %d, result = %+v", calls, result)
			}
			if (sink.events[0].TimeToFirstAccepted != nil) != tc.accepted || (sink.events[0].TimeToFirstOutput != nil) != tc.output {
				t.Fatalf("timing changed: %+v", sink.events[0])
			}
			for i, observation := range result.ProviderObservations {
				if observation.Attempt != i+1 || sink.events[i].Attempt != i+1 || sink.events[i].TaskID != "task" {
					t.Fatalf("attempt attribution = %+v", observation)
				}
			}
			if tc.name == "execution-only" && (result.ProviderObservations[0].Execution.GatewayDeployment != "failed-deployment" || result.ProviderObservations[1].Execution.GatewayDeployment != "successful-deployment") {
				t.Fatal("retried placement evidence merged")
			}
		})
	}
}

func TestProviderObservationsTerminalPaths(t *testing.T) {
	failure := errors.New("provider failure")
	for _, tc := range []struct {
		name   string
		chunks []llm.CompletionChunk
		err    error
		reason runner.TerminalReason
	}{
		{name: "completed", chunks: []llm.CompletionChunk{{Content: "answer"}}, reason: runner.TerminalCompleted},
		{name: "error", err: failure, reason: runner.TerminalError},
		{name: "cancelled", err: errors.Join(runner.ErrCancelled, context.Canceled), reason: runner.TerminalCancelled},
		{name: "maxed", chunks: []llm.CompletionChunk{runnertest.ChunkToolCall("c", "echo", `{}`)}, reason: runner.TerminalMaxIterations},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := observationClient(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
				return func(yield func(llm.CompletionChunk, error) bool) {
					for _, chunk := range tc.chunks {
						if !yield(chunk, nil) {
							return
						}
					}
					if tc.err != nil {
						yield(llm.CompletionChunk{}, tc.err)
					}
				}
			})
			registry := tools.NewRegistry(runnertest.Tool{Name: "echo", Result: "ok"})
			result := runner.New(client, runner.WithTools(registry)).Run(t.Context(), runner.TaskSpec{Prompt: "go", MaxIterations: 1})
			if result.Reason != tc.reason || !errors.Is(result.Err, tc.err) || len(result.ProviderObservations) != 1 || result.ProviderObservations[0].Attempt != 1 {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestProviderObservationsExcludeOtherRunsAndAdmissionRefusal(t *testing.T) {
	account := inference.New(inference.WithLimits(inference.Limits{Invocations: 1}))
	client := runnertest.NewClient([][]llm.CompletionChunk{{{Content: "answer"}}})
	sink := &observationSink{}
	r := runner.New(client, runner.WithSink(sink), runner.WithInferenceScope(inference.Scope{Account: account}))
	first := r.Run(t.Context(), runner.TaskSpec{Prompt: "go"})
	denied := r.Run(t.Context(), runner.TaskSpec{Prompt: "denied"})
	if len(first.ProviderObservations) != 1 || !errors.Is(denied.Err, inference.ErrBudget) || denied.ProviderObservations != nil || client.CallCount() != 1 || len(sink.events) != 1 || denied.Timing.ProviderAttempts != 0 {
		t.Fatalf("first = %+v, denied = %+v", first, denied)
	}
	if got := (runner.ProviderAttemptObservation{}).Clone(); !reflect.DeepEqual(got, runner.ProviderAttemptObservation{}) {
		t.Fatalf("Clone fabricated evidence: %+v", got)
	}
}

func TestLiteLLMRunnerObservationKnownZero(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("X-Litellm-Response-Cost", "0")
		w.Header().Set("X-Litellm-Attempted-Retries", "0")
		w.Header().Set("X-Litellm-Model-Id", "opaque-placement")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"answer\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":0,\"completion_tokens\":0,\"total_tokens\":0}}\n\ndata: [DONE]\n\n"))
	}))
	t.Cleanup(server.Close)
	provider := litellm.NewProvider("test-key", litellm.WithBaseURL(server.URL), litellm.WithModel("route-alias"), litellm.WithProtocol(llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS), litellm.WithHTTPClient(server.Client()))
	sink := &observationSink{}
	result := runner.New(runner.ClientFromProvider(provider), runner.WithSink(sink)).Run(t.Context(), runner.TaskSpec{Prompt: "go"})
	if result.Err != nil || requests.Load() != 1 || result.FinalContent != "answer" || len(result.ProviderObservations) != 1 || len(sink.events) != 1 {
		t.Fatalf("result = %+v; requests = %d", result, requests.Load())
	}
	got := result.ProviderObservations[0]
	if got.Usage == nil || *got.Usage != (llm.Usage{}) || result.TotalUsage == nil || *result.TotalUsage != (llm.Usage{}) {
		t.Fatalf("reported zero legacy usage lost: observation = %+v, total = %+v", got.Usage, result.TotalUsage)
	}
	if got.Execution == nil || got.Execution.ReportedCost != (llm.ReportedCost{Reported: true, Source: "x-litellm-response-cost"}) || got.Execution.RetryCount != (llm.ObservedCount{Known: true}) || got.Execution.GatewayDeployment != "opaque-placement" {
		t.Fatalf("execution = %+v", got.Execution)
	}
	wantPricing := &llm.PricingUsage{
		Input: llm.PricingInputUsage{
			Total:            llm.TokenQuantity{Known: true},
			WritesByLifetime: []llm.CacheWriteQuantity{{Lifetime: 5 * time.Minute}, {Lifetime: time.Hour}},
		},
		Output:                    llm.PricingOutputUsage{Total: llm.TokenQuantity{Known: true}},
		AdditionalChargesPossible: true,
	}
	if !reflect.DeepEqual(got.PricingUsage, wantPricing) {
		t.Fatalf("known-zero totals with unknown cache partition = %+v; want %+v", got.PricingUsage, wantPricing)
	}
	if !reflect.DeepEqual(got.Usage, sink.events[0].Usage) || !reflect.DeepEqual(got.PricingUsage, sink.events[0].PricingUsage) || !reflect.DeepEqual(got.Execution, sink.events[0].Execution) {
		t.Fatal("settlement lost loopback usage, pricing or execution evidence")
	}
}

func TestProviderObservationsExcludeDescendantsAndEarlierRuns(t *testing.T) {
	var r *runner.Runner
	var child runner.TaskResult
	client := observationClient(func(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
		return func(yield func(llm.CompletionChunk, error) bool) {
			prompt := req.Messages[len(req.Messages)-1].Content
			if prompt == "root" {
				child = r.Run(ctx, runner.TaskSpec{ID: "child", Depth: 1, Prompt: "child"})
			}
			yield(llm.CompletionChunk{Content: "answer", Execution: &llm.ExecutionObservation{GatewayDeployment: llm.DeploymentID(prompt)}}, nil)
		}
	})
	sink := &observationSink{}
	r = runner.New(client, runner.WithSink(sink))
	root := r.Run(t.Context(), runner.TaskSpec{ID: "root", Prompt: "root"})
	next := r.Run(t.Context(), runner.TaskSpec{ID: "root", Prompt: "next"})
	for _, tc := range []struct {
		name   string
		result runner.TaskResult
	}{{"root", root}, {"child", child}, {"next", next}} {
		if tc.result.Err != nil || len(tc.result.ProviderObservations) != 1 || tc.result.ProviderObservations[0].Attempt != 1 || tc.result.ProviderObservations[0].Execution.GatewayDeployment != llm.DeploymentID(tc.name) {
			t.Fatalf("%s = %+v", tc.name, tc.result)
		}
	}
	if len(sink.events) != 3 || sink.events[0].TaskID != "child" || sink.events[0].Depth != 1 || sink.events[1].TaskID != "root" || sink.events[1].Depth != 0 {
		t.Fatalf("root/child settlement attribution = %+v", sink.events)
	}
}

func TestProviderObservationLatestExecutionReplacesRatherThanMerges(t *testing.T) {
	client := runnertest.NewClient([][]llm.CompletionChunk{{
		{Execution: &llm.ExecutionObservation{GatewayDeployment: "first", ReportedCost: llm.ReportedCost{Amount: 2, Reported: true}}, PricingUsage: &llm.PricingUsage{Input: llm.PricingInputUsage{Total: llm.TokenQuantity{Tokens: 7, Known: true}}}},
		{Execution: &llm.ExecutionObservation{GatewayDeployment: "last"}},
		{Content: "answer"},
	}})
	result := runner.New(client).Run(t.Context(), runner.TaskSpec{Prompt: "go"})
	if result.Err != nil || len(result.ProviderObservations) != 1 {
		t.Fatalf("result = %+v", result)
	}
	got := result.ProviderObservations[0]
	if !reflect.DeepEqual(got.Execution, &llm.ExecutionObservation{GatewayDeployment: "last"}) || got.PricingUsage.Input.Total.Tokens != 7 || got.Usage != nil {
		t.Fatalf("latest independent sidecars = %+v / %+v / %+v", got.Execution, got.PricingUsage, got.Usage)
	}
}

// Mutating borrowed sidecars from a content callback distinguishes cloning
// during yield from cloning only after provider iteration returns.
type observationContentSink struct {
	observationSink
	mutateBorrowed func()
}

func (s *observationContentSink) OnContent(context.Context, runner.Content) {
	s.mutateBorrowed()
}

func TestProviderObservationOwnedBeforeContentCallbackAndCancellation(t *testing.T) {
	for _, cancelRun := range []bool{false, true} {
		name := "completed"
		if cancelRun {
			name = "cancelled"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(t.Context())
			t.Cleanup(func() { cancel(nil) })
			cause := errors.New("caller stopped")
			pricing := llm.PricingUsage{Input: llm.PricingInputUsage{WritesByLifetime: []llm.CacheWriteQuantity{{Quantity: llm.TokenQuantity{Tokens: 2, Known: true}}}}}
			execution := llm.ExecutionObservation{GatewayDeployment: "original"}
			sink := &observationContentSink{mutateBorrowed: func() {
				pricing.Input.WritesByLifetime[0].Quantity.Tokens = 500
				execution.GatewayDeployment = "callback-mutation"
				if cancelRun {
					cancel(cause)
				}
			}}
			client := observationClient(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
				return func(yield func(llm.CompletionChunk, error) bool) {
					defer func() {
						pricing.Input.WritesByLifetime[0].Quantity.Tokens = 900
						execution.GatewayDeployment = "provider-reuse"
					}()
					yield(llm.CompletionChunk{Content: "answer", PricingUsage: &pricing, Execution: &execution}, nil)
				}
			})
			result := runner.New(client, runner.WithSink(sink)).Run(ctx, runner.TaskSpec{Prompt: "go"})
			wantReason := runner.TerminalCompleted
			var wantErr error
			if cancelRun {
				wantReason, wantErr = runner.TerminalCancelled, cause
			}
			if result.Reason != wantReason || !errors.Is(result.Err, wantErr) || len(result.ProviderObservations) != 1 || len(sink.events) != 1 {
				t.Fatalf("result = %+v; settlements = %+v", result, sink.events)
			}
			got := result.ProviderObservations[0]
			if got.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens != 2 || got.Execution.GatewayDeployment != "original" || sink.events[0].Execution.GatewayDeployment != "original" {
				t.Fatalf("borrowed evidence escaped: %+v / %+v", got.PricingUsage, got.Execution)
			}
		})
	}
}

func TestProviderObservationRepeatedFramesRetryAndPricingReplacement(t *testing.T) {
	calls := 0
	var settlements []inference.Settlement
	account := inference.New(inference.WithSettlementSink(func(_ context.Context, s inference.Settlement) { settlements = append(settlements, s) }))
	client := observationClient(func(context.Context, llm.CompletionRequest) llm.CompletionStream {
		return func(yield func(llm.CompletionChunk, error) bool) {
			calls++
			chunk := llm.CompletionChunk{
				UsageReported: true, Usage: llm.Usage{PromptTokens: 3, TotalTokens: 3},
				PricingUsage: &llm.PricingUsage{Input: llm.PricingInputUsage{
					Total:            llm.TokenQuantity{Tokens: 3, Known: true},
					WritesByLifetime: []llm.CacheWriteQuantity{{Quantity: llm.TokenQuantity{Tokens: 2, Known: true}}},
				}},
				Execution: &llm.ExecutionObservation{GatewayCallID: "same-call-id", ReportedCost: llm.ReportedCost{Reported: true}},
			}
			for range 2 {
				if !yield(chunk, nil) {
					return
				}
			}
			if !yield(llm.CompletionChunk{PricingUsage: &llm.PricingUsage{}}, nil) {
				return
			}
			if calls == 1 {
				yield(llm.CompletionChunk{}, errors.New("Failed to parse tool call arguments as JSON: bad escape"))
				return
			}
			yield(llm.CompletionChunk{Content: "answer"}, nil)
		}
	})
	sink := &observationSink{}
	result := runner.New(client, runner.WithSink(sink), runner.WithInferenceScope(inference.Scope{Account: account})).Run(t.Context(), runner.TaskSpec{Prompt: "go"})
	if result.Err != nil || calls != 2 || len(result.ProviderObservations) != 2 || len(sink.events) != 2 || len(settlements) != 2 || result.TotalUsage.TotalTokens != 6 {
		t.Fatalf("result = %+v; calls = %d; settlements = %d", result, calls, len(settlements))
	}
	for i, got := range result.ProviderObservations {
		if got.Attempt != i+1 || got.Usage.TotalTokens != 3 || !reflect.DeepEqual(got.PricingUsage, &llm.PricingUsage{}) || got.Execution.GatewayCallID != "same-call-id" || !got.Execution.ReportedCost.Reported {
			t.Fatalf("attempt %d = %+v", i, got)
		}
	}
	if got := account.Snapshot(); got.Admitted != 2 || got.Settled != 2 || got.Active != 0 || got.Unknown != 0 || got.Tokens != 6 {
		t.Fatalf("snapshots summed or retries deduplicated: %+v", got)
	}
}
