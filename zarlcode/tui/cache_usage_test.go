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

func TestCacheUsageSettlesParentAndChildOnce(t *testing.T) {
	model := tui.New()
	model.SetWorkspace(t.TempDir(), "model")
	model.SetProvider("provider")
	model.SetPricing(1, 2)

	root := &llm.Usage{
		PromptTokens: 1000, CompletionTokens: 100, TotalTokens: 1100, CachedTokens: 200,
		CacheCreationTokens: 100, CacheCreation5mTokens: 30, CacheCreation1hTokens: 20,
	}
	child := &llm.Usage{
		PromptTokens: 500, CompletionTokens: 50, TotalTokens: 550, CachedTokens: 80,
		CacheCreationTokens: 70, CacheCreation5mTokens: 10, CacheCreation1hTokens: 15,
	}

	step(t, model, teasink.ConversationStartedMsg{TaskID: "root", Provider: "provider", Model: "model"})
	step(t, model, teasink.ConversationStartedMsg{TaskID: "child", Depth: 1, Provider: "provider", Model: "model"})
	step(t, model, teasink.ProviderAttemptSettledMsg{TaskID: "root", Attempt: 1, Usage: root})
	step(t, model, teasink.ProviderAttemptSettledMsg{TaskID: "root", Attempt: 1, Usage: root})
	if got := model.UsageSnapshot(); got.CacheCreationTokens != 0 {
		t.Fatalf("live attempt entered terminal totals: %+v", got)
	}

	childEnd := teasink.ConversationEndedMsg{TaskID: "child", Depth: 1, Reason: runner.TerminalCompleted, TotalUsage: child}
	rootEnd := teasink.ConversationEndedMsg{TaskID: "root", Reason: runner.TerminalCompleted, TotalUsage: root}
	step(t, model, childEnd)
	step(t, model, childEnd)
	step(t, model, rootEnd)
	step(t, model, rootEnd)

	got := model.UsageSnapshot()
	if got.CacheCreationTokens != 170 || got.CacheCreation5mTokens != 40 || got.CacheCreation1hTokens != 35 {
		t.Fatalf("cache creation totals = %+v", got)
	}
	if got.CacheCreationTokensParent != 100 || got.CacheCreation5mTokensParent != 30 || got.CacheCreation1hTokensParent != 20 {
		t.Fatalf("parent cache creation totals = %+v", got)
	}
	if got.Cached != 280 || got.CachedParent != 200 || got.CacheDetailsVersion != 1 {
		t.Fatalf("cache read/coverage totals = %+v", got)
	}
	if math.Abs(got.CostParentUSD-0.9) > 1e-12 || math.Abs(got.CostUSD-1.35) > 1e-12 {
		t.Fatalf("known cost subtotal = parent %.3f/session %.3f, want .900/1.350", got.CostParentUSD, got.CostUSD)
	}
	if got.CacheSavedUSD != 0 {
		t.Fatalf("new usage fabricated cache savings = %.3f", got.CacheSavedUSD)
	}
	if got.UnpricedCacheWriteTasks != 2 {
		t.Fatalf("unpriced cache-write tasks = %d, want 2", got.UnpricedCacheWriteTasks)
	}

	diagnostics := model.CacheDiagnostics()
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostic targets = %+v", diagnostics)
	}
	d := diagnostics[0]
	if d.Provider != "provider" || d.Model != "model" || d.SettledTasks != 2 ||
		d.RootTasks != 1 || d.ChildTasks != 1 || d.CacheCreationUnknownTTL != 95 ||
		math.Abs(d.KnownCostUSD-1.35) > 1e-12 || math.Abs(d.CacheReadShare-float64(280)/1500) > 1e-12 {
		t.Fatalf("target diagnostics = %+v", d)
	}

	rendered := ansi.Strip(strings.Join(model.RenderCockpitLines(180), "\n"))
	for _, want := range []string{"provider/model", "base-rate estimate $1.35", "cache read 280 (19%)", "create 170", "read rate unknown 2", "write rate unknown 2"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("cache report missing %q:\n%s", want, rendered)
		}
	}
}

func TestCacheUsageSnapshotJSONCoverageCompatibility(t *testing.T) {
	tests := []struct {
		name string
		blob string
		want tui.SessionUsageSnapshot
	}{
		{
			name: "legacy",
			blob: `{"turns":2,"in":1000,"out":100,"cached":300,"in_parent":1000,"out_parent":100,"cached_parent":300}`,
			want: tui.SessionUsageSnapshot{Turns: 2, In: 1000, Out: 100, Cached: 300, InParent: 1000, OutParent: 100, CachedParent: 300, CostAccountingVersion: 1, CacheDiagnosticsVersion: 1, CacheDiagnosticsLegacy: true},
		},
		{
			name: "mixed legacy and observed",
			blob: `{"turns":3,"in":1500,"out":150,"cached":350,"cache_creation_tokens":90,"cache_creation_5m_tokens":20,"cache_creation_1h_tokens":30,"in_parent":1000,"out_parent":100,"cached_parent":300,"cache_creation_tokens_parent":40,"cache_creation_5m_tokens_parent":10}`,
			want: tui.SessionUsageSnapshot{Turns: 3, In: 1500, Out: 150, Cached: 350, CacheCreationTokens: 90, CacheCreation5mTokens: 20, CacheCreation1hTokens: 30, InParent: 1000, OutParent: 100, CachedParent: 300, CacheCreationTokensParent: 40, CacheCreation5mTokensParent: 10, CostAccountingVersion: 1, CacheDiagnosticsVersion: 1, CacheDiagnosticsLegacy: true},
		},
		{
			name: "fully covered",
			blob: `{"turns":1,"in":500,"out":50,"cached":50,"cache_creation_tokens":60,"cache_creation_5m_tokens":20,"cache_creation_1h_tokens":10,"in_parent":500,"out_parent":50,"cached_parent":50,"cache_creation_tokens_parent":60,"cache_creation_5m_tokens_parent":20,"cache_creation_1h_tokens_parent":10,"cache_details_version":1}`,
			want: tui.SessionUsageSnapshot{Turns: 1, In: 500, Out: 50, Cached: 50, CacheCreationTokens: 60, CacheCreation5mTokens: 20, CacheCreation1hTokens: 10, InParent: 500, OutParent: 50, CachedParent: 50, CacheCreationTokensParent: 60, CacheCreation5mTokensParent: 20, CacheCreation1hTokensParent: 10, CacheDetailsVersion: 1, CostAccountingVersion: 1, CacheDiagnosticsVersion: 1, CacheDiagnosticsLegacy: true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var decoded tui.SessionUsageSnapshot
			if err := json.Unmarshal([]byte(tc.blob), &decoded); err != nil {
				t.Fatal(err)
			}
			var state tui.RunState
			state.RestoreUsage(decoded)
			got := state.UsageSnapshot()
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("restored snapshot = %+v, want %+v", got, tc.want)
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			var roundTrip tui.SessionUsageSnapshot
			if err := json.Unmarshal(encoded, &roundTrip); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(roundTrip, tc.want) {
				t.Fatalf("JSON round trip = %+v, want %+v", roundTrip, tc.want)
			}
		})
	}
}
