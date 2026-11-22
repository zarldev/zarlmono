package teasink_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
)

func TestAttemptObservationCallbackOwnsSidecars(t *testing.T) {
	send, snapshot := recordingSend()
	sink := teasink.New(send)
	t.Cleanup(sink.Close)
	usage := &llm.Usage{PromptTokens: 10}
	pricing := &llm.PricingUsage{Input: llm.PricingInputUsage{WritesByLifetime: []llm.CacheWriteQuantity{{Quantity: llm.TokenQuantity{Known: true, Tokens: 4}}}}}
	execution := &llm.ExecutionObservation{GatewayDeployment: "deployment-a", ReportedCost: llm.ReportedCost{Reported: true}}
	card := &llm.RateCard{Reference: llm.RateCardReference{ID: "policy", Revision: "r1", Entry: "entry"}, Currency: "EUR",
		CacheWrites: []llm.CacheWriteRate{{Lifetime: time.Hour, Rate: llm.TokenRate{Known: true, PerMillionTokens: 2}}}}
	wantCard := card.Clone()
	wantPricing, wantExecution := pricing.Clone(), execution.Clone()
	sink.OnContent(t.Context(), runner.Content{TaskID: "root", Delta: "before metadata"})
	sink.OnProviderAttemptSettled(t.Context(), runner.ProviderAttemptSettled{TaskID: "root", Attempt: 1,
		Usage: usage, PricingUsage: pricing, Execution: execution, RateCard: card, Err: errors.New("private provider detail"), Reason: runner.TerminalError})
	card.Reference.Revision = "source-mutated"
	card.CacheWrites[0].Rate.PerMillionTokens = 999
	usage.PromptTokens = 900
	pricing.Input.WritesByLifetime[0].Quantity.Tokens = 900
	execution.GatewayDeployment = "mutated"
	execution.ReportedCost.Currency = "USD"
	sink.OnProviderAttemptSettled(t.Context(), runner.ProviderAttemptSettled{TaskID: "root", Attempt: 2})
	sink.OnProviderAttemptSettled(t.Context(), runner.ProviderAttemptSettled{TaskID: "root", Attempt: 3, PricingUsage: &llm.PricingUsage{}, Execution: &llm.ExecutionObservation{}})
	sink.Drain()
	messages := snapshot()
	if len(messages) != 4 {
		t.Fatalf("messages = %d", len(messages))
	}
	if content, ok := messages[0].(teasink.ContentMsg); !ok || content.Delta != "before metadata" {
		t.Fatalf("text ordering = %+v", messages[0])
	}
	got := messages[1].(teasink.ProviderAttemptSettledMsg)
	if got.TaskID != "root" || got.Attempt != 1 || got.Reason != runner.TerminalError || got.Usage.PromptTokens != 10 || !reflect.DeepEqual(got.PricingUsage, &wantPricing) || !reflect.DeepEqual(got.Execution, &wantExecution) || !reflect.DeepEqual(got.RateCard, &wantCard) {
		t.Fatalf("attempt snapshot = %+v", got)
	}
	got.RateCard.Reference.Revision = "consumer-mutated"
	got.RateCard.CacheWrites[0].Rate.PerMillionTokens = 888
	if card.Reference.Revision != "source-mutated" || card.CacheWrites[0].Rate.PerMillionTokens != 999 {
		t.Fatal("consumer changed source card")
	}
	got.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 800
	got.Execution.GatewayDeployment = "consumer-mutated"
	if pricing.Input.WritesByLifetime[0].Quantity.Tokens != 900 || execution.GatewayDeployment != "mutated" {
		t.Fatal("consumer changed provider-owned sidecars")
	}
	absent := messages[2].(teasink.ProviderAttemptSettledMsg)
	if absent.Usage != nil || absent.PricingUsage != nil || absent.Execution != nil || absent.RateCard != nil {
		t.Fatalf("absence = %+v", absent)
	}
	unknown := messages[3].(teasink.ProviderAttemptSettledMsg)
	if unknown.Usage != nil || unknown.PricingUsage == nil || unknown.Execution == nil || unknown.Execution.ReportedCost.Reported {
		t.Fatalf("unknown = %+v", unknown)
	}
}

func TestAttemptObservationLiteLLMRunnerDelivery(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected protocol path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Litellm-Model-Id", "observed-placement")
		w.Header().Set("X-Litellm-Response-Cost", "0")
		_, _ = fmt.Fprint(w, `data: {"id":"chat_1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":2,"total_tokens":10}}

data: [DONE]

`)
	}))
	t.Cleanup(server.Close)
	send, snapshot := recordingSend()
	sink := teasink.New(send)
	t.Cleanup(sink.Close)
	provider := litellm.NewProvider("loopback-test-key", litellm.WithBaseURL(server.URL), litellm.WithModel("configured-alias"), litellm.WithHTTPClient(server.Client()))
	result := runner.New(provider, runner.WithSink(sink)).Run(t.Context(), runner.TaskSpec{ID: "root", Prompt: "question"})
	if result.Err != nil {
		t.Fatalf("Run: %v", result.Err)
	}
	sink.Drain()
	var attempts []teasink.ProviderAttemptSettledMsg
	for _, message := range snapshot() {
		if attempt, ok := message.(teasink.ProviderAttemptSettledMsg); ok {
			attempts = append(attempts, attempt)
		}
	}
	if requests.Load() != 1 || len(attempts) != 1 {
		t.Fatalf("requests/attempts = %d/%d", requests.Load(), len(attempts))
	}
	got := attempts[0]
	if got.TaskID != "root" || got.Attempt != 1 || got.Execution == nil || got.Execution.GatewayDeployment != "observed-placement" || got.Execution.RequestedModel != "configured-alias" || got.Usage == nil || *got.Usage != (llm.Usage{PromptTokens: 8, CompletionTokens: 2, TotalTokens: 10}) {
		t.Fatalf("delivered metadata = %+v", got)
	}
	wantPricing := &llm.PricingUsage{
		Input: llm.PricingInputUsage{
			Total:            llm.TokenQuantity{Tokens: 8, Known: true},
			WritesByLifetime: []llm.CacheWriteQuantity{{Lifetime: 5 * time.Minute}, {Lifetime: time.Hour}},
		},
		Output:                    llm.PricingOutputUsage{Total: llm.TokenQuantity{Tokens: 2, Known: true}},
		AdditionalChargesPossible: true,
	}
	if !reflect.DeepEqual(got.PricingUsage, wantPricing) {
		t.Fatalf("delivered pricing with unknown cache partition = %+v; want %+v", got.PricingUsage, wantPricing)
	}
	if len(result.ProviderObservations) != 1 || !reflect.DeepEqual(got.Usage, result.ProviderObservations[0].Usage) || !reflect.DeepEqual(got.PricingUsage, result.ProviderObservations[0].PricingUsage) || !reflect.DeepEqual(got.Execution, result.ProviderObservations[0].Execution) {
		t.Fatal("delivery differs from retained runner evidence")
	}
	cost := got.Execution.ReportedCost
	if !cost.Reported || cost.Amount != 0 || cost.Currency != "" || cost.CoverageKnown {
		t.Fatalf("reported zero/unknown currency = %+v", cost)
	}
}
