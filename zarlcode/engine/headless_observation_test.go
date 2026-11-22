package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
)

// observationClient counts the actual codec yields without adding metadata
// yields, replaying the stream, or changing the provider's response.
type observationClient struct {
	client runner.Client
	yields int
}

func (c *observationClient) Complete(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		c.client.Complete(ctx, req)(func(chunk llm.CompletionChunk, err error) bool {
			c.yields++
			return yield(chunk, err)
		})
	}
}

func TestHeadlessObservationLiteLLMRunnerReport(t *testing.T) {
	t.Parallel()
	const alias = "Company/Production-Alias-V7"
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var request struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Model != alias || !request.Stream {
			t.Errorf("request = %+v", request)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Litellm-Model-Id", "opaque-deployment")
		w.Header().Set("X-Litellm-Model-Name", "observed-model")
		w.Header().Set("X-Litellm-Version", "gateway-runtime")
		w.Header().Set("X-Litellm-Call-Id", "gateway-call")
		w.Header().Set("X-Litellm-Attempted-Retries", "0")
		w.Header().Set("X-Litellm-Attempted-Fallbacks", "2")
		w.Header().Set("X-Litellm-Response-Cost", "0")
		// A currency-looking unrecognized header must not imply USD.
		w.Header().Set("X-Litellm-Cost-Currency", "USD")
		_, _ = io.WriteString(w, "data: "+`{"id":"chat_1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}]}`+"\n\n")
		_, _ = io.WriteString(w, "data: "+`{"id":"chat_1","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`+"\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	provider := litellm.NewProvider("ordinary-test-key", litellm.WithBaseURL(server.URL), litellm.WithModel(alias), litellm.WithHTTPClient(server.Client()))
	client := &observationClient{client: runner.ClientFromProvider(provider)}
	card := reportConfiguredCard(t, engine.RateCardTarget{Provider: "gateway", Model: alias, BaseURL: server.URL, Protocol: llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS}, "loopback-r1", "EUR", false)
	r := runner.New(client, runner.WithMaxIterations(2), runner.WithRateCard(card))
	res := r.Run(t.Context(), runner.TaskSpec{ID: "loopback-observation", Prompt: "hello"})
	if res.Err != nil || res.Reason != runner.TerminalCompleted {
		t.Fatalf("Run = %s, %v", res.Reason, res.Err)
	}
	report := engine.Report(res)
	if calls.Load() != 1 || client.yields != 2 || len(report.ProviderObservations) != 1 {
		t.Fatalf("requests=%d yields=%d observations=%d", calls.Load(), client.yields, len(report.ProviderObservations))
	}
	got := report.ProviderObservations[0]
	if got.Attempt != 1 || got.LegacyUsage == nil || got.LegacyUsage.TotalTokens != 7 {
		t.Fatalf("observation = %+v", got)
	}
	wantPricing := &engine.HeadlessPricingUsage{
		Input: engine.HeadlessPricingInput{
			Total:            engine.HeadlessTokenQuantity{Tokens: 5, Known: true},
			WritesByLifetime: []engine.HeadlessCacheWriteQuantity{{LifetimeNS: int64(5 * time.Minute)}, {LifetimeNS: int64(time.Hour)}},
		},
		Output:                    engine.HeadlessPricingOutput{Total: engine.HeadlessTokenQuantity{Tokens: 2, Known: true}},
		AdditionalChargesPossible: true,
	}
	if !reflect.DeepEqual(got.NormalizedPricingUsage, wantPricing) {
		t.Fatalf("normalized usage with unknown cache partition = %+v; want %+v", got.NormalizedPricingUsage, wantPricing)
	}
	if got.RateCard == nil || got.RateCard.Currency != "EUR" || got.ConfiguredEstimate == nil || got.ConfiguredEstimate.Complete || got.ConfiguredEstimate.HasKnownSubtotal || len(got.ConfiguredEstimate.Issues) == 0 {
		t.Fatalf("real codec incompleteness lost: basis %+v estimate %+v", got.RateCard, got.ConfiguredEstimate)
	}
	e := got.Execution
	if e == nil {
		t.Fatal("execution absent")
	}
	if e.RequestedModel != alias || e.GatewayDeployment != "opaque-deployment" || e.ReportedModel != "observed-model" || e.GatewayVersion != "gateway-runtime" || e.ReportedVersion != "" || e.GatewayCallID != "gateway-call" || e.ProviderCallID != "" || e.Route != "" {
		t.Fatalf("execution identities = %+v", e)
	}
	if e.RetryCount != (engine.HeadlessObservedCount{Known: true}) || e.FallbackCount != (engine.HeadlessObservedCount{Count: 2, Known: true}) || e.ReportedCost != (engine.HeadlessReportedCost{Reported: true, Source: "x-litellm-response-cost"}) {
		t.Fatalf("execution counts/cost = %+v", e)
	}
	if report.TotalUsage == nil || report.TotalUsage.TotalTokens != 7 || report.PromptTokens != 5 || report.CompletionTokens != 2 || report.Timing.ProviderAttempts != 1 {
		t.Fatalf("legacy accounting changed: %+v", report)
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"amount":0`, `"reported":true`, `"currency":""`, `"coverage_known":false`, `"requested_model":"` + alias + `"`, `"gateway_deployment":"opaque-deployment"`, `"reported_model":"observed-model"`} {
		if !strings.Contains(string(data), field) {
			t.Errorf("JSON missing %s: %s", field, data)
		}
	}
	res.ProviderObservations[0].Execution.GatewayDeployment = "result mutation"
	if e.GatewayDeployment != "opaque-deployment" {
		t.Fatal("report aliases runner result")
	}
}

// scriptedObservationClient owns borrowed evidence that it mutates immediately
// after yield returns. Each Complete is one attempt, not one report/redrive.
type scriptedObservationClient struct {
	calls     int
	terminal  error
	usage     llm.Usage
	pricing   llm.PricingUsage
	execution llm.ExecutionObservation
}

func (c *scriptedObservationClient) Complete(_ context.Context, _ llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		c.calls++
		c.usage = llm.Usage{PromptTokens: c.calls * 10}
		c.pricing = llm.PricingUsage{Input: llm.PricingInputUsage{Total: llm.TokenQuantity{Tokens: int64(c.calls * 10), Known: true}, WritesByLifetime: []llm.CacheWriteQuantity{{Lifetime: 5 * time.Minute, Quantity: llm.TokenQuantity{Known: true}}}}}
		c.execution = llm.ExecutionObservation{GatewayDeployment: llm.DeploymentID(fmt.Sprintf("deployment-%d", c.calls)), ReportedCost: llm.ReportedCost{Amount: float64(c.calls), Reported: true}}
		content := ""
		if c.calls > 1 || c.terminal != nil {
			content = "accepted output"
		}
		if !yield(llm.CompletionChunk{Content: content, PricingUsage: &c.pricing, Execution: &c.execution}, nil) {
			return
		}
		c.pricing.Input.WritesByLifetime[0].Quantity.Tokens = 999
		c.execution.GatewayDeployment = "borrowed source mutation"
		if !yield(llm.CompletionChunk{UsageReported: true, Usage: c.usage}, nil) {
			return
		}
		if c.terminal != nil {
			yield(llm.CompletionChunk{}, c.terminal)
		} else if c.calls == 1 {
			yield(llm.CompletionChunk{}, errors.New("Failed to parse tool call arguments as JSON: bad escape"))
		}
	}
}

func TestHeadlessObservationAttemptsAndFailure(t *testing.T) {
	t.Parallel()
	terminal := errors.New("stream interrupted")
	for _, tt := range []struct {
		name     string
		err      error
		attempts int
		reason   runner.TerminalReason
	}{
		{name: "recovery retains failed attempt", attempts: 2, reason: runner.TerminalCompleted},
		{name: "terminal failure retains evidence", err: terminal, attempts: 1, reason: runner.TerminalError},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client := &scriptedObservationClient{terminal: tt.err}
			r := runner.New(client, runner.WithMaxIterations(3))
			res := r.Run(t.Context(), runner.TaskSpec{ID: "scripted-observation", Prompt: "hello"})
			if res.Reason != tt.reason || !errors.Is(res.Err, tt.err) {
				t.Fatalf("Run = %s, %v", res.Reason, res.Err)
			}
			report := engine.Report(res)
			if client.calls != tt.attempts || len(report.ProviderObservations) != tt.attempts {
				t.Fatalf("calls=%d observations=%d", client.calls, len(report.ProviderObservations))
			}
			for i, got := range report.ProviderObservations {
				if got.Attempt != i+1 || got.LegacyUsage == nil || got.LegacyUsage.PromptTokens != (i+1)*10 || got.NormalizedPricingUsage.Input.Total.Tokens != int64((i+1)*10) || got.NormalizedPricingUsage.Input.WritesByLifetime[0].Quantity.Tokens != 0 || !got.NormalizedPricingUsage.Input.WritesByLifetime[0].Quantity.Known || got.Execution.GatewayDeployment != fmt.Sprintf("deployment-%d", i+1) || got.Execution.ReportedCost.Amount != float64(i+1) {
					t.Fatalf("attempt %d = %+v", i+1, got)
				}
			}
			if report.TotalUsage == nil || report.TotalUsage.PromptTokens != tt.attempts*(tt.attempts+1)*5 {
				t.Fatalf("legacy total changed: %+v", report.TotalUsage)
			}
			// Projection and source each own all mutable references; no cost sum
			// or cross-Run/session collector is involved.
			report.ProviderObservations[0].NormalizedPricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 888
			report.ProviderObservations[0].Execution.GatewayDeployment = "report mutation"
			if res.ProviderObservations[0].PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens != 0 || res.ProviderObservations[0].Execution.GatewayDeployment != "deployment-1" {
				t.Fatal("report aliases retained runner result")
			}
			fresh := engine.Report(res)
			if fresh.ProviderObservations[0].Execution.GatewayDeployment != "deployment-1" {
				t.Fatal("later Report retained earlier report mutation")
			}
			// A reused Runner models the separate Runs of verified re-drive:
			// the next report must not contain evidence from the earlier Run.
			next := engine.Report(r.Run(t.Context(), runner.TaskSpec{ID: "next-run", Prompt: "again"}))
			if len(next.ProviderObservations) != 1 || next.ProviderObservations[0].Attempt != 1 || next.ProviderObservations[0].Execution.GatewayDeployment != fmt.Sprintf("deployment-%d", tt.attempts+1) {
				t.Fatalf("next Run inherited prior observations: %+v", next.ProviderObservations)
			}
		})
	}
}
