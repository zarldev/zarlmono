package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
)

func reportConfiguredCard(t *testing.T, target engine.RateCardTarget, revision, currency string, zero bool) llm.RateCard {
	t.Helper()
	wire := fmt.Sprintf(`{"version":1,"target":{"provider":%q,"model":%q,"base_url":%q,"protocol":%q},"card":{
 "id":"local-policy","revision":%q,"entry":"exact-entry","pricing_provider":"operator","currency":%q,
 "billing":"token-metered","applicable":true,"token_only":true,
 "fresh_input_per_million":2,"cache_read_per_million":0.5,"output_per_million":3,
 "cache_writes":[{"lifetime_seconds":300,"per_million":4},{"lifetime_seconds":3600,"per_million":6}]}}`,
		target.Provider, target.Model, target.BaseURL, target.Protocol.String(), revision, currency)
	if zero {
		wire = strings.NewReplacer(
			`"fresh_input_per_million":2`, `"fresh_input_per_million":0`,
			`"cache_read_per_million":0.5`, `"cache_read_per_million":0`,
			`"output_per_million":3`, `"output_per_million":0`,
			`"per_million":4`, `"per_million":0`,
			`"per_million":6`, `"per_million":0`,
		).Replace(wire)
	}
	config, err := engine.ParseRateCardConfig([]byte(wire), time.Date(2026, time.November, 12, 1, 2, 3, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	card, err := config.Select(target)
	if err != nil {
		t.Fatal(err)
	}
	return card
}

// This explicit synthetic normalized fixture asserts disjoint token coverage.
// Real codecs are tested separately and need not supply this complete evidence.
func reportNormalizedUsage() llm.PricingUsage {
	q := func(n int64) llm.TokenQuantity { return llm.TokenQuantity{Tokens: n, Known: true} }
	return llm.PricingUsage{
		Input: llm.PricingInputUsage{Total: q(1_000_000), CacheRead: q(200_000), CacheWrite: q(400_000), Other: q(0), PartitionKnown: true,
			WritesByLifetime: []llm.CacheWriteQuantity{{Lifetime: 5 * time.Minute, Quantity: q(100_000)}, {Lifetime: time.Hour, Quantity: q(300_000)}}},
		Output: llm.PricingOutputUsage{Total: q(500_000), Other: q(0)},
	}
}

type configuredReportClient struct {
	pricing *llm.PricingUsage
	calls   int
	retry   bool
}

func (c *configuredReportClient) Complete(context.Context, llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		c.calls++
		content := "answer"
		if c.retry && c.calls == 1 {
			content = ""
		}
		if !yield(llm.CompletionChunk{Content: content, UsageReported: true,
			Usage: llm.Usage{PromptTokens: 1_000_000, CompletionTokens: 500_000}, PricingUsage: c.pricing,
			Execution: &llm.ExecutionObservation{ReportedCost: llm.ReportedCost{Reported: true, Amount: 999}},
		}, nil) {
			return
		}
		if c.retry && c.calls == 1 {
			yield(llm.CompletionChunk{}, errors.New("Failed to parse tool call arguments as JSON: bad escape"))
		}
	}
}

func TestConfiguredRateCardReportPresenceAndCoverage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                                                              string
		noCard, noUsage, zero, unknown, extra, uncovered, unknownLifetime bool
		subtotal                                                          float64
		complete, known                                                   bool
	}{
		{name: "mixed fresh read and two write lifetimes", subtotal: 4.6, complete: true, known: true},
		{name: "known zero rates", zero: true, complete: true, known: true},
		{name: "missing normalized usage", noUsage: true},
		{name: "no selected card", noCard: true},
		{name: "unknown quantities", unknown: true},
		{name: "additional charges stay incomplete", extra: true, subtotal: 4.6, known: true},
		{name: "card coverage assertion absent", uncovered: true, subtotal: 4.6, known: true},
		{name: "unknown write lifetime", unknownLifetime: true, subtotal: 2.8, known: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			card := reportConfiguredCard(t, engine.RateCardTarget{Provider: "gateway", Model: "Team/Alias", Protocol: llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS}, "r1", "EUR", tc.zero)
			usage := reportNormalizedUsage()
			if tc.unknown {
				usage = llm.PricingUsage{}
			}
			usage.AdditionalChargesPossible = tc.extra
			if tc.uncovered {
				card.TokenOnly = false
			}
			if tc.unknownLifetime {
				usage.Input.WritesByLifetime[1].Lifetime = 0
			}
			client := &configuredReportClient{pricing: &usage}
			if tc.noUsage {
				client.pricing = nil
			}
			r := runner.New(client)
			if !tc.noCard {
				r = runner.New(client, runner.WithRateCard(card))
			}
			result := r.Run(t.Context(), runner.TaskSpec{Prompt: "hello"})
			if result.Err != nil {
				t.Fatal(result.Err)
			}
			report := engine.Report(result)
			if len(report.ProviderObservations) != 1 {
				t.Fatalf("observations = %d", len(report.ProviderObservations))
			}
			got := report.ProviderObservations[0]
			if tc.noCard {
				if got.RateCard != nil || got.ConfiguredEstimate != nil || got.ConfiguredEstimateUnavailable != "" {
					t.Fatalf("unselected basis inferred: %+v", got)
				}
				return
			}
			if got.RateCard.Reference.Revision != "r1" || got.RateCard.Currency != "EUR" || got.RateCard.PricingProvider != "operator" || !got.RateCard.Applicable || got.RateCard.TokenOnly == tc.uncovered || got.RateCard.Provenance.Revision != card.Provenance.Revision || got.RateCard.Provenance.Source != card.Provenance.Source.String() || got.RateCard.Provenance.ObservedAt != card.Provenance.ObservedAt || got.RateCard.CacheWrites[1].LifetimeNS != int64(time.Hour) {
				t.Fatalf("basis projection = %+v", got.RateCard)
			}
			if tc.noUsage {
				if got.ConfiguredEstimate != nil || got.ConfiguredEstimateUnavailable != "missing normalized pricing usage" || got.LegacyUsage == nil {
					t.Fatalf("legacy usage fabricated estimate: %+v", got)
				}
				data, err := json.Marshal(report)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(data), `"configured_estimate":null`) || !strings.Contains(string(data), `"configured_estimate_unavailable":"missing normalized pricing usage"`) {
					t.Fatalf("absence lost in JSON: %s", data)
				}
				return
			}
			e := got.ConfiguredEstimate
			if e == nil || e.Complete != tc.complete || e.HasKnownSubtotal != tc.known || math.Abs(e.Subtotal-tc.subtotal) > 1e-12 || e.Currency != "EUR" || e.Reference != got.RateCard.Reference || e.Provenance != got.RateCard.Provenance {
				t.Fatalf("estimate = %+v", e)
			}
			expected := backends.EstimatePrice(card, usage)
			if e.InputAmount != expected.InputAmount || e.OutputAmount != expected.OutputAmount || len(e.Issues) != len(expected.Issues) {
				t.Fatalf("estimator projection differs: %+v / %+v", e, expected)
			}
			for i, issue := range expected.Issues {
				if e.Issues[i] != (engine.HeadlessPricingIssue{Kind: issue.Kind.String(), Field: issue.Field, LifetimeNS: int64(issue.Lifetime)}) {
					t.Fatalf("issue = %+v", e.Issues[i])
				}
			}
			if got.Execution.ReportedCost.Amount != 999 || got.Execution.ReportedCost.Currency != "" || report.TotalUsage.PromptTokens != 1_000_000 {
				t.Fatal("estimate changed reported currency or legacy totals")
			}
			data, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{`"per_million_tokens":`, `"lifetime_ns":`, `"has_known_subtotal":`, `"complete":`, `"input_amount":`, `"output_amount":`, `"subtotal":`, `"reference":`, `"provenance":`} {
				if !strings.Contains(string(data), field) {
					t.Errorf("missing %s in %s", field, data)
				}
			}
			var decoded engine.HeadlessReport
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(report, decoded) {
				t.Fatal("JSON round trip lost presence or basis")
			}
		})
	}
}

func TestConfiguredRateCardReportRetriesAndOwnership(t *testing.T) {
	t.Parallel()
	card := reportConfiguredCard(t, engine.RateCardTarget{Provider: "gateway", Model: "Team/Alias", Protocol: llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS}, "before", "EUR", false)
	usage := reportNormalizedUsage()
	client := &configuredReportClient{pricing: &usage, retry: true}
	r := runner.New(client, runner.WithRateCard(card))
	card.Reference.Revision = "after"
	card.CacheWrites[0].Rate.PerMillionTokens = 999
	result := r.Run(t.Context(), runner.TaskSpec{Prompt: "hello"})
	if result.Err != nil || len(result.ProviderObservations) != 2 {
		t.Fatalf("result = %+v", result)
	}
	report := engine.Report(result)
	for _, attempt := range report.ProviderObservations {
		if attempt.RateCard.Reference.Revision != "before" || math.Abs(attempt.ConfiguredEstimate.Subtotal-4.6) > 1e-12 {
			t.Fatalf("retry repriced: %+v", attempt)
		}
	}
	want := engine.Report(result)
	result.ProviderObservations[0].RateCard.CacheWrites[0].Rate.PerMillionTokens = 888
	result.ProviderObservations[0].RateCard.Reference.Revision = "result-mutated"
	result.ProviderObservations[0].PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 777
	if !reflect.DeepEqual(report, want) {
		t.Fatal("report borrows result")
	}
	report.ProviderObservations[0].RateCard.CacheWrites[0].Rate.PerMillionTokens = 666
	report.ProviderObservations[0].ConfiguredEstimate.Reference.Revision = "report-mutated"
	if result.ProviderObservations[0].RateCard.CacheWrites[0].Rate.PerMillionTokens != 888 || report.ProviderObservations[1].RateCard.CacheWrites[0].Rate.PerMillionTokens != 4 || report.ProviderObservations[1].ConfiguredEstimate.Reference.Revision != "before" {
		t.Fatal("report, result and attempts share basis")
	}
	// Incomplete estimates own issue slices independently on every projection.
	incomplete := engine.Report(runner.TaskResult{ProviderObservations: []runner.ProviderAttemptObservation{{RateCard: &card, PricingUsage: &llm.PricingUsage{}}}})
	again := engine.Report(runner.TaskResult{ProviderObservations: []runner.ProviderAttemptObservation{{RateCard: &card, PricingUsage: &llm.PricingUsage{}}}})
	incomplete.ProviderObservations[0].ConfiguredEstimate.Issues[0].Field = "mutated"
	if again.ProviderObservations[0].ConfiguredEstimate.Issues[0].Field == "mutated" {
		t.Fatal("projections share estimate issues")
	}
}

func TestConfiguredRateCardReportLiteLLMCostNeverAdded(t *testing.T) {
	t.Parallel()
	for _, header := range []string{"1.25", "2.5"} {
		t.Run("header="+header, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("X-Litellm-Response-Cost", header)
				_, _ = fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}

data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":20,"cost":1.25}}

data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":20,"cost":1.25}}

data: [DONE]

`)
			}))
			t.Cleanup(server.Close)
			target := engine.RateCardTarget{Provider: "gateway", Model: "Team/Alias", BaseURL: server.URL, Protocol: llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS}
			card := reportConfiguredCard(t, target, "loopback", "EUR", false)
			provider := litellm.NewProvider("loopback-only", litellm.WithBaseURL(server.URL), litellm.WithModel(string(target.Model)), litellm.WithHTTPClient(server.Client()))
			result := runner.New(provider, runner.WithRateCard(card)).Run(t.Context(), runner.TaskSpec{Prompt: "hello"})
			if result.Err != nil {
				t.Fatal(result.Err)
			}
			report := engine.Report(result)
			if len(report.ProviderObservations) != 1 {
				t.Fatalf("observations=%d", len(report.ProviderObservations))
			}
			got := report.ProviderObservations[0]
			if got.Execution.ReportedCost.Amount != 1.25 || got.Execution.ReportedCost.Currency != "" || !got.Execution.ReportedCost.Reported || got.Execution.ReportedCost.CoverageKnown {
				t.Fatalf("reported cost = %+v", got.Execution.ReportedCost)
			}
			estimate := backends.EstimatePrice(card, *result.ProviderObservations[0].PricingUsage)
			if got.ConfiguredEstimate.Complete || got.ConfiguredEstimate.HasKnownSubtotal != estimate.HasKnownSubtotal || got.ConfiguredEstimate.Subtotal != estimate.Subtotal || got.ConfiguredEstimate.Currency != "EUR" || got.ConfiguredEstimate.Subtotal == 1.25 {
				t.Fatalf("reported cost became configured estimate: %+v", got.ConfiguredEstimate)
			}
			if report.TotalUsage.PromptTokens != 100 || report.TotalUsage.CompletionTokens != 20 {
				t.Fatalf("cumulative frames summed: %+v", report.TotalUsage)
			}
		})
	}
}

func TestConfiguredRateCardReportIndependentBasisRevisions(t *testing.T) {
	t.Parallel()
	var observations []runner.ProviderAttemptObservation
	for i, currency := range []string{"EUR", "USD"} {
		card := reportConfiguredCard(t, engine.RateCardTarget{Provider: "gateway", Model: "Team/Alias", Protocol: llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS}, fmt.Sprintf("r%d", i+1), currency, i == 1)
		usage := reportNormalizedUsage()
		result := runner.New(&configuredReportClient{pricing: &usage}, runner.WithRateCard(card)).Run(t.Context(), runner.TaskSpec{Prompt: "hello"})
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		observation := result.ProviderObservations[0]
		observation.Attempt = i + 1
		observations = append(observations, observation)
	}
	report := engine.Report(runner.TaskResult{ProviderObservations: observations})
	for i, currency := range []string{"EUR", "USD"} {
		got := report.ProviderObservations[i]
		if got.ConfiguredEstimate.Reference.Revision != fmt.Sprintf("r%d", i+1) || got.ConfiguredEstimate.Currency != currency || !got.ConfiguredEstimate.HasKnownSubtotal || !got.ConfiguredEstimate.Complete {
			t.Fatalf("attempt %d = %+v", i+1, got)
		}
	}
	if math.Abs(report.ProviderObservations[0].ConfiguredEstimate.Subtotal-4.6) > 1e-12 || report.ProviderObservations[1].ConfiguredEstimate.Subtotal != 0 {
		t.Fatal("later revision repriced earlier attempt")
	}
}

func TestConfiguredRateCardReportUnknownBasis(t *testing.T) {
	t.Parallel()
	card := llm.RateCard{Provenance: llm.MetadataProvenance{Snapshot: "source-snapshot", ExpiresAt: time.Date(2026, time.November, 13, 0, 0, 0, 0, time.UTC)}}
	usage := reportNormalizedUsage()
	report := engine.Report(runner.TaskResult{ProviderObservations: []runner.ProviderAttemptObservation{{Attempt: 1, RateCard: &card, PricingUsage: &usage}}})
	got := report.ProviderObservations[0]
	if got.ConfiguredEstimate == nil || got.ConfiguredEstimate.HasKnownSubtotal || got.ConfiguredEstimate.Complete || got.ConfiguredEstimate.Currency != "" || len(got.ConfiguredEstimate.Issues) == 0 || got.RateCard.Applicable || got.RateCard.TokenOnly || got.RateCard.Provenance.Snapshot != "source-snapshot" || got.RateCard.Provenance.ExpiresAt != card.Provenance.ExpiresAt {
		t.Fatalf("unknown basis/expiry lost: %+v", got)
	}
}
