package engine_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestReportUsesExactRequestAccounting(t *testing.T) {
	t.Parallel()

	res := runner.TaskResult{
		Reason:       runner.TerminalError,
		Cause:        runner.TerminalCauseStreamIdle,
		Iterations:   1,
		Duration:     2 * time.Second,
		SystemPrompt: "one two three",
		LastUsage: &llm.Usage{
			PromptTokens:     120,
			CachedTokens:     80,
			CompletionTokens: 7,
		},
		ToolSurface: runner.ToolSurface{
			Count:       4,
			JSONBytes:   2048,
			Fingerprint: "abc123",
		},
	}
	got := engine.Report(res)
	if got.PromptBytes != len(res.SystemPrompt) || got.PromptWords != 3 {
		t.Errorf("prompt accounting = %d bytes/%d words", got.PromptBytes, got.PromptWords)
	}
	if got.ToolCount != 4 || got.ToolJSONBytes != 2048 || got.ToolFingerprint != "abc123" {
		t.Errorf("tool accounting = %+v", got)
	}
	if got.PromptTokens != 120 || got.CachedTokens != 80 || got.CompletionTokens != 7 {
		t.Errorf("usage accounting = %+v", got)
	}
	if got.TerminalCause != runner.TerminalCauseStreamIdle {
		t.Errorf("terminal cause = %q", got.TerminalCause)
	}
}

func TestReportSeparatesTotalsUnknownUsageAndResidual(t *testing.T) {
	t.Parallel()
	res := runner.TaskResult{
		Duration:   10 * time.Second,
		LastUsage:  &llm.Usage{PromptTokens: 5},
		TotalUsage: &llm.Usage{PromptTokens: 12, CompletionTokens: 3, TotalTokens: 17},
		Timing:     runner.TaskTiming{ProviderAttempts: 3, AttemptsWithUsage: 2, ProviderDuration: 4 * time.Second, CallbackDuration: time.Second, RequestPreparationDuration: time.Second, ToolDispatchDuration: 2 * time.Second},
	}
	got := engine.Report(res)
	if got.PromptTokens != 5 || got.TotalUsage.PromptTokens != 12 || got.TotalUsage.CompletionTokens != 3 || got.TotalUsage.TotalTokens != 17 || got.UnattributedDuration != 3*time.Second || got.Timing.ProviderAttempts != 3 {
		t.Fatalf("report = %+v", got)
	}
	for _, usage := range []*llm.Usage{nil, {}} {
		got := engine.Report(runner.TaskResult{TotalUsage: usage})
		if (got.TotalUsage == nil) != (usage == nil) {
			t.Fatal("lost usage presence")
		}
		data, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), `"total_usage":null`) != (usage == nil) {
			t.Fatalf("json = %s", data)
		}
	}
}

func TestReportProjectsUsageDetailsJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		usage    *llm.Usage
		want     engine.HeadlessUsage
		wantJSON map[string]string
	}{
		{
			name: "nonzero details retain inclusive totals",
			usage: &llm.Usage{
				PromptTokens: 120, CachedTokens: 30, CompletionTokens: 40, TotalTokens: 160,
				CacheCreationTokens: 50, CacheCreation5mTokens: 20, CacheCreation1hTokens: 25,
				ReasoningTokens: 15, ReasoningTokensReported: true,
			},
			want: engine.HeadlessUsage{
				PromptTokens: 120, CachedTokens: 30, CompletionTokens: 40, TotalTokens: 160,
				CacheCreationTokens: 50, CacheCreation5mTokens: 20, CacheCreation1hTokens: 25,
				ReasoningTokens: 15, ReasoningTokensReported: true,
			},
			wantJSON: map[string]string{
				"prompt_tokens": "120", "cached_tokens": "30", "completion_tokens": "40", "total_tokens": "160",
				"cache_creation_tokens": "50", "cache_creation_5m_tokens": "20", "cache_creation_1h_tokens": "25",
				"reasoning_tokens": "15", "reasoning_tokens_reported": "true",
			},
		},
		{
			name:  "reasoning explicitly reported zero",
			usage: &llm.Usage{ReasoningTokensReported: true},
			want:  engine.HeadlessUsage{ReasoningTokensReported: true},
			wantJSON: map[string]string{
				"prompt_tokens": "0", "cached_tokens": "0", "completion_tokens": "0", "total_tokens": "0",
				"cache_creation_tokens": "0", "cache_creation_5m_tokens": "0", "cache_creation_1h_tokens": "0",
				"reasoning_tokens": "0", "reasoning_tokens_reported": "true",
			},
		},
		{
			name:  "all zero usage with reasoning unreported",
			usage: &llm.Usage{},
			wantJSON: map[string]string{
				"prompt_tokens": "0", "cached_tokens": "0", "completion_tokens": "0", "total_tokens": "0",
				"cache_creation_tokens": "0", "cache_creation_5m_tokens": "0", "cache_creation_1h_tokens": "0",
				"reasoning_tokens": "0", "reasoning_tokens_reported": "false",
			},
		},
		{
			name: "usage unreported",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := engine.Report(runner.TaskResult{
				LastUsage:  &llm.Usage{PromptTokens: 11, CachedTokens: 7, CompletionTokens: 3},
				TotalUsage: tt.usage,
			})
			if got.PromptTokens != 11 || got.CachedTokens != 7 || got.CompletionTokens != 3 {
				t.Errorf("latest-context usage = %d prompt/%d cached/%d completion, want 11/7/3", got.PromptTokens, got.CachedTokens, got.CompletionTokens)
			}
			switch {
			case tt.usage == nil:
				if got.TotalUsage != nil {
					t.Errorf("unreported total usage = %+v, want nil", got.TotalUsage)
				}
			case got.TotalUsage == nil:
				t.Error("reported total usage is nil")
			case *got.TotalUsage != tt.want:
				t.Errorf("total usage = %+v, want %+v", *got.TotalUsage, tt.want)
			}

			data, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]string{
				"prompt_tokens": "11", "cached_tokens": "7", "completion_tokens": "3",
			} {
				if string(fields[key]) != want {
					t.Errorf("JSON %s = %s, want %s from LastUsage", key, fields[key], want)
				}
			}
			if tt.usage == nil {
				if string(fields["total_usage"]) != "null" {
					t.Errorf("JSON total_usage = %s, want null", fields["total_usage"])
				}
				return
			}
			var usageFields map[string]json.RawMessage
			if err := json.Unmarshal(fields["total_usage"], &usageFields); err != nil {
				t.Fatal(err)
			}
			if len(usageFields) != len(tt.wantJSON) {
				t.Errorf("JSON total_usage has %d fields, want %d: %s", len(usageFields), len(tt.wantJSON), fields["total_usage"])
			}
			for key, want := range tt.wantJSON {
				if string(usageFields[key]) != want {
					t.Errorf("JSON total_usage.%s = %s, want %s", key, usageFields[key], want)
				}
			}
		})
	}
}

func TestReportProviderObservationPresenceJSON(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name                                   string
		observation                            runner.ProviderAttemptObservation
		wantLegacy, wantPricing, wantExecution bool
		known                                  bool
	}{
		{name: "absent"},
		{name: "legacy does not imply normalized", observation: runner.ProviderAttemptObservation{Usage: &llm.Usage{}}, wantLegacy: true},
		{name: "present unknown", observation: runner.ProviderAttemptObservation{PricingUsage: &llm.PricingUsage{}, Execution: &llm.ExecutionObservation{}}, wantPricing: true, wantExecution: true},
		{name: "known zero", observation: runner.ProviderAttemptObservation{
			Usage:        &llm.Usage{ReasoningTokensReported: true},
			PricingUsage: &llm.PricingUsage{Input: llm.PricingInputUsage{Total: llm.TokenQuantity{Known: true}}},
			Execution:    &llm.ExecutionObservation{RetryCount: llm.ObservedCount{Known: true}, ReportedCost: llm.ReportedCost{Reported: true}},
		}, wantLegacy: true, wantPricing: true, wantExecution: true, known: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.observation.Attempt = 7
			report := engine.Report(runner.TaskResult{ProviderObservations: []runner.ProviderAttemptObservation{tt.observation}})
			data, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			var wire struct {
				Observations []map[string]json.RawMessage `json:"provider_observations"`
			}
			if err := json.Unmarshal(data, &wire); err != nil {
				t.Fatal(err)
			}
			if len(wire.Observations) != 1 {
				t.Fatalf("observations JSON = %s", data)
			}
			fields := wire.Observations[0]
			if len(fields) != 7 || string(fields["attempt"]) != "7" || string(fields["configured_estimate_unavailable"]) != `""` {
				t.Fatalf("observation fields = %s", data)
			}
			for key, present := range map[string]bool{"legacy_usage": tt.wantLegacy, "normalized_pricing_usage": tt.wantPricing, "execution": tt.wantExecution, "rate_card": false, "configured_estimate": false} {
				raw, ok := fields[key]
				if !ok || (string(raw) != "null") != present {
					t.Errorf("%s = %s, want present %t", key, raw, present)
				}
			}
			if tt.wantPricing {
				var pricing struct {
					Input struct {
						Total struct {
							Tokens int64 `json:"tokens"`
							Known  bool  `json:"known"`
						} `json:"total"`
					} `json:"input"`
				}
				if err := json.Unmarshal(fields["normalized_pricing_usage"], &pricing); err != nil {
					t.Fatal(err)
				}
				if pricing.Input.Total.Tokens != 0 || pricing.Input.Total.Known != tt.known {
					t.Errorf("pricing presence = %+v", pricing)
				}
			}
			if tt.wantExecution {
				var execution map[string]json.RawMessage
				if err := json.Unmarshal(fields["execution"], &execution); err != nil {
					t.Fatal(err)
				}
				var cost map[string]json.RawMessage
				if err := json.Unmarshal(execution["reported_cost"], &cost); err != nil {
					t.Fatal(err)
				}
				wantReported := "false"
				if tt.known {
					wantReported = "true"
				}
				for key, want := range map[string]string{"amount": "0", "currency": `""`, "reported": wantReported, "coverage_known": "false", "source": `""`} {
					if string(cost[key]) != want {
						t.Errorf("reported_cost.%s = %s, want %s", key, cost[key], want)
					}
				}
			}
		})
	}
	for _, observations := range [][]runner.ProviderAttemptObservation{nil, {}} {
		data, err := json.Marshal(engine.Report(runner.TaskResult{ProviderObservations: observations}))
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		want := "null"
		if observations != nil {
			want = "[]"
		}
		if string(fields["provider_observations"]) != want {
			t.Errorf("provider_observations = %s, want %s", fields["provider_observations"], want)
		}
	}
}

func TestReportProviderObservationOwnedProjection(t *testing.T) {
	t.Parallel()
	u := llm.Usage{PromptTokens: 20, CachedTokens: 3, CompletionTokens: 9, TotalTokens: 29, CacheCreationTokens: 5, CacheCreation5mTokens: 2, CacheCreation1hTokens: 3, ReasoningTokens: 4, ReasoningTokensReported: true}
	p := llm.PricingUsage{
		Input: llm.PricingInputUsage{Total: llm.TokenQuantity{Tokens: 20, Known: true}, CacheRead: llm.TokenQuantity{Tokens: 3, Known: true}, CacheWrite: llm.TokenQuantity{Tokens: 5, Known: true}, Other: llm.TokenQuantity{Known: true}, PartitionKnown: true,
			WritesByLifetime: []llm.CacheWriteQuantity{{Lifetime: 5 * time.Minute, Quantity: llm.TokenQuantity{Tokens: 2, Known: true}}, {Lifetime: time.Hour, Quantity: llm.TokenQuantity{Tokens: 3, Known: true}}, {}}},
		Output: llm.PricingOutputUsage{Total: llm.TokenQuantity{Tokens: 9, Known: true}, Other: llm.TokenQuantity{Known: true}}, AdditionalChargesPossible: true,
	}
	e := llm.ExecutionObservation{Route: "route", RequestedModel: "alias", GatewayDeployment: "deployment", GatewayVersion: "runtime", ReportedModel: "model", ReportedVersion: "version", GatewayCallID: "gateway-call", ProviderCallID: "provider-call",
		PromptCacheReadTokens: llm.TokenQuantity{Known: true}, PromptCacheWriteTokens: llm.TokenQuantity{Tokens: 5, Known: true}, RetryCount: llm.ObservedCount{Known: true}, FallbackCount: llm.ObservedCount{Count: 2, Known: true}, AffinitySupport: llm.Supports.SUPPORTSUPPORTED,
		ReportedCost: llm.ReportedCost{Amount: 1.25, Currency: "EUR", Reported: true, CoverageKnown: true, Source: "gateway"}}
	res := runner.TaskResult{Timing: runner.TaskTiming{TimeToFirstOutput: new(time.Second)}, ProviderObservations: []runner.ProviderAttemptObservation{{Attempt: 2, Usage: &u, PricingUsage: &p, Execution: &e}}}
	report := engine.Report(res)
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded engine.HeadlessReport
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	got := decoded.ProviderObservations[0]
	if got.Attempt != 2 || *got.LegacyUsage != (engine.HeadlessUsage{PromptTokens: 20, CachedTokens: 3, CompletionTokens: 9, TotalTokens: 29, CacheCreationTokens: 5, CacheCreation5mTokens: 2, CacheCreation1hTokens: 3, ReasoningTokens: 4, ReasoningTokensReported: true}) {
		t.Fatalf("legacy projection = %+v", got)
	}
	pricing := got.NormalizedPricingUsage
	if pricing.Input.Total.Tokens != 20 || pricing.Input.CacheRead.Tokens != 3 || pricing.Input.CacheWrite.Tokens != 5 || !pricing.Input.Other.Known || !pricing.Input.PartitionKnown || pricing.Output.Total.Tokens != 9 || !pricing.Output.Other.Known || !pricing.AdditionalChargesPossible {
		t.Fatalf("pricing projection = %+v", pricing)
	}
	writes := pricing.Input.WritesByLifetime
	if len(writes) != 3 || writes[0].LifetimeNS != int64(5*time.Minute) || writes[0].Quantity.Tokens != 2 || !writes[0].Quantity.Known || writes[1].LifetimeNS != int64(time.Hour) || writes[1].Quantity.Tokens != 3 || !writes[1].Quantity.Known || writes[2] != (engine.HeadlessCacheWriteQuantity{}) {
		t.Fatalf("write lifetime projection = %+v", writes)
	}
	wantExecution := engine.HeadlessExecution{Route: "route", RequestedModel: "alias", GatewayDeployment: "deployment", GatewayVersion: "runtime", ReportedModel: "model", ReportedVersion: "version", GatewayCallID: "gateway-call", ProviderCallID: "provider-call", PromptCacheReadTokens: engine.HeadlessTokenQuantity{Known: true}, PromptCacheWriteTokens: engine.HeadlessTokenQuantity{Tokens: 5, Known: true}, RetryCount: engine.HeadlessObservedCount{Known: true}, FallbackCount: engine.HeadlessObservedCount{Count: 2, Known: true}, AffinitySupport: llm.Supports.SUPPORTSUPPORTED.String(), ReportedCost: engine.HeadlessReportedCost{Amount: 1.25, Currency: "EUR", Reported: true, CoverageKnown: true, Source: "gateway"}}
	if *got.Execution != wantExecution {
		t.Fatalf("execution = %+v, want %+v", *got.Execution, wantExecution)
	}
	// Every mutable level is independently owned, in both directions.
	*res.Timing.TimeToFirstOutput = 2 * time.Second
	u.PromptTokens = 999
	p.Input.WritesByLifetime[0].Quantity.Tokens = 999
	e.GatewayDeployment = "mutated source"
	res.ProviderObservations[0].Attempt = 999
	res.ProviderObservations = append(res.ProviderObservations, runner.ProviderAttemptObservation{})
	after, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(data) {
		t.Fatalf("source mutation changed report: %s", after)
	}
	*report.Timing.TimeToFirstOutput = 3 * time.Second
	if *res.Timing.TimeToFirstOutput != 2*time.Second {
		t.Fatal("report aliases timing source")
	}
	report.ProviderObservations[0].LegacyUsage.CachedTokens = 999
	report.ProviderObservations[0].NormalizedPricingUsage.Input.WritesByLifetime[1].Quantity.Tokens = 999
	report.ProviderObservations[0].Execution.ProviderCallID = "mutated report"
	if u.CachedTokens != 3 || p.Input.WritesByLifetime[1].Quantity.Tokens != 3 || e.ProviderCallID != "provider-call" {
		t.Fatal("report aliases source")
	}
	fresh := engine.Report(res)
	fresh.ProviderObservations[0].NormalizedPricingUsage.Input.WritesByLifetime[1].Quantity.Tokens = 888
	if report.ProviderObservations[0].NormalizedPricingUsage.Input.WritesByLifetime[1].Quantity.Tokens != 999 {
		t.Fatal("reports alias each other")
	}
}
