package tui_test

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestCacheDiagnosticsAttributesDifferentPhysicalTargets(t *testing.T) {
	model := tui.New()
	model.SetWorkspace(t.TempDir(), "root-model")
	model.SetProvider("root-provider")
	model.SetPricing(1, 2)
	usage := &llm.Usage{PromptTokens: 100, CompletionTokens: 10, CachedTokens: 20, CacheCreationTokens: 10}

	step(t, model, teasink.ConversationStartedMsg{TaskID: "root", Provider: "root-provider", Model: "root-model"})
	step(t, model, teasink.ConversationStartedMsg{TaskID: "child", Depth: 1, Provider: "child-provider", Model: "child-model"})
	step(t, model, teasink.ConversationEndedMsg{TaskID: "child", Depth: 1, Reason: runner.TerminalError, TotalUsage: usage})
	step(t, model, teasink.ConversationEndedMsg{TaskID: "root", Reason: runner.TerminalCompleted, TotalUsage: usage})

	got := model.CacheDiagnostics()
	if len(got) != 2 || got[0].Provider != "child-provider" || got[1].Provider != "root-provider" {
		t.Fatalf("sorted physical targets = %+v", got)
	}
	if got[0].ChildTasks != 1 || got[0].BaseRateUnknownTasks != 1 || got[0].KnownCostUSD != 0 {
		t.Fatalf("child target coverage = %+v", got[0])
	}
	if got[1].RootTasks != 1 || got[1].BaseRateUnknownTasks != 0 || math.Abs(got[1].KnownCostUSD-.09) > 1e-12 {
		t.Fatalf("root target known subtotal = %+v", got[1])
	}
}

func TestCacheDiagnosticsDistinguishesAbsentUsageFromReportedZero(t *testing.T) {
	model := tui.New()
	model.SetWorkspace(t.TempDir(), "model")
	model.SetProvider("provider")
	model.SetPricing(1, 2)

	step(t, model, teasink.ConversationStartedMsg{TaskID: "absent", Provider: "provider", Model: "model"})
	step(t, model, teasink.ConversationEndedMsg{TaskID: "absent", Reason: runner.TerminalCancelled})
	step(t, model, teasink.ConversationStartedMsg{TaskID: "zero", Provider: "provider", Model: "model"})
	step(t, model, teasink.ConversationEndedMsg{TaskID: "zero", Reason: runner.TerminalCompleted, TotalUsage: &llm.Usage{}})

	got := model.CacheDiagnostics()
	if len(got) != 1 || got[0].SettledTasks != 2 || got[0].MissingUsageTasks != 1 ||
		got[0].ReportedZeroUsageTasks != 1 || got[0].CacheReadShareKnown {
		t.Fatalf("usage absence diagnostics = %+v", got)
	}
}

func TestCacheDiagnosticsSnapshotRoundTripOwnsTargetSlice(t *testing.T) {
	original := tui.SessionUsageSnapshot{
		Turns:                   2,
		CostAccountingVersion:   1,
		CostUSD:                 1.25,
		LegacyCostUSD:           .75,
		LegacyCacheSavedUSD:     .5,
		CacheDiagnosticsVersion: 1,
		CacheTargets: []tui.CacheTargetDiagnostics{
			{Provider: "llamacpp", Model: "local", BillingClass: "local", SettledTasks: 1},
			{Provider: "openai-codex", Model: "subscription", BillingClass: "subscription", SettledTasks: 1},
		},
	}
	var state tui.RunState
	state.RestoreUsage(original)
	original.CacheTargets[0].Provider = "mutated"

	first := state.UsageSnapshot()
	if first.CacheDiagnosticsLegacy || first.CacheTargets[0].Provider != "llamacpp" || first.CacheTargets[1].Provider != "openai-codex" {
		t.Fatalf("restored diagnostics = %+v", first)
	}
	first.CacheTargets[0].Provider = "aliased"
	second := state.UsageSnapshot()
	if second.CacheTargets[0].Provider != "llamacpp" {
		t.Fatalf("snapshot target slice aliases state: %+v", second.CacheTargets)
	}

	blob, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip tui.SessionUsageSnapshot
	if err := json.Unmarshal(blob, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(roundTrip, second) {
		t.Fatalf("JSON round trip = %+v, want %+v", roundTrip, second)
	}
	if roundTrip.CostAccountingVersion != 1 || roundTrip.CostUSD != 1.25 ||
		roundTrip.LegacyCostUSD != .75 || roundTrip.LegacyCacheSavedUSD != .5 || roundTrip.CacheSavedUSD != 0 {
		t.Fatalf("versioned and legacy costs changed: %+v", roundTrip)
	}
}

func TestCacheDiagnosticsMigratesUnversionedCostsToLegacyDisplayOnly(t *testing.T) {
	var state tui.RunState
	state.RestoreUsage(tui.SessionUsageSnapshot{
		Turns: 2, CostUSD: 1.25, CostParentUSD: 1, CacheSavedUSD: .5,
	})

	got := state.UsageSnapshot()
	if got.CostAccountingVersion != 1 || got.CostUSD != 0 || got.CostParentUSD != 0 || got.CacheSavedUSD != 0 ||
		got.LegacyCostUSD != 1.25 || got.LegacyCostParentUSD != 1 || got.LegacyCacheSavedUSD != .5 {
		t.Fatalf("migrated costs = %+v", got)
	}
}

func TestCacheDiagnosticsPricesKnownRateComponentsOnly(t *testing.T) {
	tests := []struct {
		name                string
		input, output       float64
		wantCost            float64
		wantUnknownBaseTask int
	}{
		{name: "missing output", input: 1, wantCost: 1, wantUnknownBaseTask: 1},
		{name: "missing input", output: 2, wantCost: .2, wantUnknownBaseTask: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			model := tui.New()
			model.SetWorkspace(t.TempDir(), "model")
			model.SetProvider("provider")
			model.SetPricing(tc.input, tc.output)
			usage := &llm.Usage{PromptTokens: 1000, CompletionTokens: 100}
			step(t, model, teasink.ConversationStartedMsg{TaskID: "root", Provider: "provider", Model: "model"})
			step(t, model, teasink.ConversationEndedMsg{TaskID: "root", Reason: runner.TerminalCompleted, TotalUsage: usage})

			got := model.CacheDiagnostics()[0]
			if math.Abs(got.KnownCostUSD-tc.wantCost) > 1e-12 || got.BaseRateUnknownTasks != tc.wantUnknownBaseTask {
				t.Fatalf("partial-rate diagnostics = %+v", got)
			}
		})
	}
}

func TestCacheDiagnosticsMarksPromptTierEstimateIncomplete(t *testing.T) {
	model := tui.New()
	model.SetWorkspace(t.TempDir(), "gpt-6-astra")
	model.SetProvider("openai")
	model.SetPricing(.01, .05)
	usage := &llm.Usage{PromptTokens: 300_000, CompletionTokens: 1000}
	step(t, model, teasink.ConversationStartedMsg{TaskID: "root", Provider: "openai", Model: "gpt-6-astra"})
	step(t, model, teasink.ConversationEndedMsg{TaskID: "root", Reason: runner.TerminalCompleted, TotalUsage: usage})

	got := model.CacheDiagnostics()[0]
	if got.PricingTierUnknownTasks != 1 || got.BaseRateUnknownTasks != 0 {
		t.Fatalf("tier-sensitive diagnostics = %+v", got)
	}
	rendered := ansi.Strip(strings.Join(model.RenderCockpitLines(180), "\n"))
	if !strings.Contains(rendered, "prompt pricing tier unavailable 1") || !strings.Contains(rendered, "base-rate estimate") {
		t.Fatalf("tier caveat missing:\n%s", rendered)
	}
}
