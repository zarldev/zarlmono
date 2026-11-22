package engine_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestHeadlessCompactionObservationReport(t *testing.T) {
	t.Parallel()
	usage := &llm.Usage{PromptTokens: 20, CachedTokens: 3, CompletionTokens: 9, TotalTokens: 29,
		CacheCreationTokens: 5, CacheCreation5mTokens: 2, CacheCreation1hTokens: 3,
		ReasoningTokens: 4, ReasoningTokensReported: true}
	pricing := &llm.PricingUsage{
		Input: llm.PricingInputUsage{
			Total: llm.TokenQuantity{Tokens: 20, Known: true}, CacheRead: llm.TokenQuantity{Tokens: 3, Known: true},
			CacheWrite: llm.TokenQuantity{Tokens: 5, Known: true}, Other: llm.TokenQuantity{Known: true}, PartitionKnown: true,
			WritesByLifetime: []llm.CacheWriteQuantity{{Lifetime: 5 * time.Minute, Quantity: llm.TokenQuantity{Tokens: 2, Known: true}}, {Lifetime: time.Hour, Quantity: llm.TokenQuantity{Tokens: 3, Known: true}}},
		},
		Output:                    llm.PricingOutputUsage{Total: llm.TokenQuantity{Tokens: 9, Known: true}, Other: llm.TokenQuantity{Tokens: 1, Known: true}},
		AdditionalChargesPossible: true,
	}
	execution := &llm.ExecutionObservation{
		Route: "aux-route", RequestedModel: "aux-alias", GatewayDeployment: "aux-deployment", GatewayVersion: "gateway-v1",
		ReportedModel: "aux-observed-model", ReportedVersion: "model-v2", GatewayCallID: "gateway-call", ProviderCallID: "provider-call",
		PromptCacheReadTokens: llm.TokenQuantity{Tokens: 3, Known: true}, PromptCacheWriteTokens: llm.TokenQuantity{Tokens: 5, Known: true},
		RetryCount: llm.ObservedCount{Known: true}, FallbackCount: llm.ObservedCount{Count: 1, Known: true},
		AffinitySupport: llm.Supports.SUPPORTSUPPORTED,
		ReportedCost:    llm.ReportedCost{Amount: 0.25, Currency: "EUR", Reported: true, CoverageKnown: true, Source: "provider"},
	}
	main := runner.TaskResult{
		LastUsage:  &llm.Usage{PromptTokens: 100, CachedTokens: 10, CompletionTokens: 15},
		TotalUsage: &llm.Usage{PromptTokens: 200, CompletionTokens: 30, TotalTokens: 230},
		Duration:   10 * time.Second, Iterations: 2, Reason: runner.TerminalCompleted,
		Timing:               runner.TaskTiming{ProviderAttempts: 2, AttemptsWithUsage: 2, ProviderDuration: 4 * time.Second},
		ProviderObservations: []runner.ProviderAttemptObservation{{Attempt: 1, Execution: &llm.ExecutionObservation{RequestedModel: "main-alias", GatewayDeployment: "main-deployment"}, RateCard: &llm.RateCard{Currency: "USD"}}},
	}
	baseline := engine.Report(main)
	res := main
	res.CompactionObservations = []compact.Inference{
		{Provider: "aux-provider", Model: "aux-configured-model", Duration: 250 * time.Millisecond, Usage: usage, PricingUsage: pricing, Execution: execution},
		{},
		{Provider: "unknown-sidecars", PricingUsage: &llm.PricingUsage{}, Execution: &llm.ExecutionObservation{}},
		{Provider: "reported-zero", Usage: &llm.Usage{ReasoningTokensReported: true},
			PricingUsage: &llm.PricingUsage{Input: llm.PricingInputUsage{Total: llm.TokenQuantity{Known: true}, WritesByLifetime: []llm.CacheWriteQuantity{}}},
			Execution:    &llm.ExecutionObservation{RetryCount: llm.ObservedCount{Known: true}, ReportedCost: llm.ReportedCost{Reported: true}}},
	}
	report := engine.Report(res)
	if len(report.CompactionObservations) != 4 {
		t.Fatalf("observations = %+v", report.CompactionObservations)
	}
	withoutAux := report
	withoutAux.CompactionObservations = nil
	if !reflect.DeepEqual(withoutAux, baseline) {
		t.Fatal("auxiliary observations changed main accounting, timing or evidence")
	}
	got := report.CompactionObservations[0]
	want := engine.HeadlessCompactionObservation{
		Provider: "aux-provider", Model: "aux-configured-model", Duration: 250 * time.Millisecond,
		LegacyUsage: &engine.HeadlessUsage{PromptTokens: 20, CachedTokens: 3, CompletionTokens: 9, TotalTokens: 29,
			CacheCreationTokens: 5, CacheCreation5mTokens: 2, CacheCreation1hTokens: 3, ReasoningTokens: 4, ReasoningTokensReported: true},
		NormalizedPricingUsage: &engine.HeadlessPricingUsage{
			Input: engine.HeadlessPricingInput{Total: engine.HeadlessTokenQuantity{Tokens: 20, Known: true}, CacheRead: engine.HeadlessTokenQuantity{Tokens: 3, Known: true},
				CacheWrite: engine.HeadlessTokenQuantity{Tokens: 5, Known: true}, Other: engine.HeadlessTokenQuantity{Known: true}, PartitionKnown: true,
				WritesByLifetime: []engine.HeadlessCacheWriteQuantity{{LifetimeNS: int64(5 * time.Minute), Quantity: engine.HeadlessTokenQuantity{Tokens: 2, Known: true}}, {LifetimeNS: int64(time.Hour), Quantity: engine.HeadlessTokenQuantity{Tokens: 3, Known: true}}}},
			Output: engine.HeadlessPricingOutput{Total: engine.HeadlessTokenQuantity{Tokens: 9, Known: true}, Other: engine.HeadlessTokenQuantity{Tokens: 1, Known: true}}, AdditionalChargesPossible: true},
		Execution: &engine.HeadlessExecution{Route: "aux-route", RequestedModel: "aux-alias", GatewayDeployment: "aux-deployment", GatewayVersion: "gateway-v1",
			ReportedModel: "aux-observed-model", ReportedVersion: "model-v2", GatewayCallID: "gateway-call", ProviderCallID: "provider-call",
			PromptCacheReadTokens: engine.HeadlessTokenQuantity{Tokens: 3, Known: true}, PromptCacheWriteTokens: engine.HeadlessTokenQuantity{Tokens: 5, Known: true},
			RetryCount: engine.HeadlessObservedCount{Known: true}, FallbackCount: engine.HeadlessObservedCount{Count: 1, Known: true}, AffinitySupport: "supported",
			ReportedCost: engine.HeadlessReportedCost{Amount: 0.25, Currency: "EUR", Reported: true, CoverageKnown: true, Source: "provider"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("auxiliary projection = %+v; want %+v", got, want)
	}

	// Mutating every borrowed sidecar after Report must not affect JSON output.
	usage.PromptTokens = 999
	pricing.Input.WritesByLifetime[0].Quantity.Tokens = 999
	execution.GatewayDeployment = "source-mutated"
	execution.ReportedCost.Currency = "USD"
	res.CompactionObservations[0].Model = "source-mutated"
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded engine.HeadlessReport
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.CompactionObservations[0], want) {
		t.Fatal("JSON projection aliases borrowed inference")
	}
	var raw struct {
		Observations []map[string]json.RawMessage `json:"compaction_observations"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for i, observation := range raw.Observations {
		if len(observation) != 6 {
			t.Fatalf("observation %d includes fabricated attempt, estimate or other fields: %s", i, data)
		}
		for _, field := range []string{"provider", "model", "duration", "legacy_usage", "normalized_pricing_usage", "execution"} {
			if _, ok := observation[field]; !ok {
				t.Errorf("observation %d missing %s", i, field)
			}
		}
	}
	for _, field := range []string{"legacy_usage", "normalized_pricing_usage", "execution"} {
		if string(raw.Observations[1][field]) != "null" {
			t.Errorf("absent %s = %s", field, raw.Observations[1][field])
		}
	}
	unknown, zero := decoded.CompactionObservations[2], decoded.CompactionObservations[3]
	if unknown.LegacyUsage != nil || unknown.NormalizedPricingUsage == nil || unknown.Execution == nil || unknown.NormalizedPricingUsage.Input.Total.Known || unknown.Execution.ReportedCost.Reported {
		t.Fatalf("unknown sidecars = %+v", unknown)
	}
	if zero.LegacyUsage == nil || !zero.LegacyUsage.ReasoningTokensReported || !zero.NormalizedPricingUsage.Input.Total.Known || !zero.Execution.ReportedCost.Reported || zero.Execution.ReportedCost.Amount != 0 || zero.Execution.ReportedCost.Currency != "" || zero.Execution.ReportedCost.CoverageKnown || !zero.Execution.RetryCount.Known || zero.NormalizedPricingUsage.Input.WritesByLifetime == nil {
		t.Fatalf("reported zero = %+v", zero)
	}
	if unknown.Model != "" || unknown.Execution.RequestedModel != "" || unknown.Execution.GatewayDeployment != "" {
		t.Fatal("unknown auxiliary identity borrowed main identity")
	}
	got.LegacyUsage.PromptTokens = 888
	got.NormalizedPricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 888
	got.Execution.GatewayDeployment = "consumer-mutated"
	if usage.PromptTokens != 999 || pricing.Input.WritesByLifetime[0].Quantity.Tokens != 999 || execution.GatewayDeployment != "source-mutated" {
		t.Fatal("report mutation escaped to source")
	}
	if again := engine.Report(res); again.CompactionObservations[0].LegacyUsage.PromptTokens != 999 || again.CompactionObservations[0].Execution.GatewayDeployment != "source-mutated" {
		t.Fatal("later report reused earlier snapshot")
	}
}

func TestHeadlessCompactionObservationCollectionPresence(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name         string
		observations []compact.Inference
		want         string
	}{
		{name: "absent", want: "null"},
		{name: "empty", observations: []compact.Inference{}, want: "[]"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data, err := json.Marshal(engine.Report(runner.TaskResult{CompactionObservations: tt.observations}))
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if string(fields["compaction_observations"]) != tt.want {
				t.Fatalf("compaction_observations = %s; want %s", fields["compaction_observations"], tt.want)
			}
		})
	}
}

func TestHeadlessCompactionObservationIndependentSidecars(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		inference compact.Inference
		want      engine.HeadlessCompactionObservation
	}{
		{name: "legacy only", inference: compact.Inference{Usage: &llm.Usage{}},
			want: engine.HeadlessCompactionObservation{LegacyUsage: &engine.HeadlessUsage{}}},
		{name: "pricing only", inference: compact.Inference{PricingUsage: &llm.PricingUsage{}},
			want: engine.HeadlessCompactionObservation{NormalizedPricingUsage: &engine.HeadlessPricingUsage{}}},
		{name: "execution only", inference: compact.Inference{Execution: &llm.ExecutionObservation{ReportedCost: llm.ReportedCost{Reported: true}}},
			want: engine.HeadlessCompactionObservation{Execution: &engine.HeadlessExecution{AffinitySupport: llm.Support{}.String(), ReportedCost: engine.HeadlessReportedCost{Reported: true}}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data, err := json.Marshal(engine.Report(runner.TaskResult{CompactionObservations: []compact.Inference{tt.inference}}))
			if err != nil {
				t.Fatal(err)
			}
			var decoded engine.HeadlessReport
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			if len(decoded.CompactionObservations) != 1 || !reflect.DeepEqual(decoded.CompactionObservations[0], tt.want) {
				t.Fatalf("independent evidence = %s; want %+v", data, tt.want)
			}
		})
	}
}
