package tui_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func compactionEvidence() compact.Inference {
	return compact.Inference{
		Provider: "aux-provider", Model: "aux-model", Duration: time.Second,
		Usage: &llm.Usage{PromptTokens: 100, CompletionTokens: 10, CachedTokens: 20,
			CacheCreationTokens: 30, CacheCreation5mTokens: 10, CacheCreation1hTokens: 20},
		PricingUsage: &llm.PricingUsage{Input: llm.PricingInputUsage{
			CacheRead: llm.TokenQuantity{Known: true, Tokens: 20}, CacheWrite: llm.TokenQuantity{Known: true, Tokens: 30},
			WritesByLifetime: []llm.CacheWriteQuantity{
				{Lifetime: 5 * time.Minute, Quantity: llm.TokenQuantity{Known: true, Tokens: 10}},
				{Lifetime: time.Hour, Quantity: llm.TokenQuantity{Known: true, Tokens: 20}},
			},
		}},
		Execution: &llm.ExecutionObservation{
			GatewayDeployment: "placement", GatewayVersion: "gw-v1", ReportedModel: "served", ReportedVersion: "model-v2",
			ReportedCost: llm.ReportedCost{Reported: true}, PromptCacheReadTokens: llm.TokenQuantity{Known: true},
		},
	}
}

func TestCompactionObservationsManualBoundAndRootCarry(t *testing.T) {
	ui := tui.New()
	if got := ui.CompactionObservations(); got != nil {
		t.Fatalf("initial diagnostics = %+v", got)
	}
	for i := range 5 {
		step(t, ui, teasink.CompactionSettledMsg{CompactionSettled: runner.CompactionSettled{
			TaskID: taskscope.ID(fmt.Sprintf("manual-%d", i)), Depth: i, Inference: compact.Inference{Provider: "aux", Model: "small"},
		}})
		got := ui.CompactionObservations()
		if len(got) != min(i+1, 3) || got[len(got)-1].TaskID != fmt.Sprintf("manual-%d", i) {
			t.Fatalf("settlement %d: %+v", i, got)
		}
	}
	want := ui.CompactionObservations()
	for i, got := range want {
		if got.TaskID != fmt.Sprintf("manual-%d", i+2) || got.Depth != i+2 || got.Inference.Provider != "aux" || got.Inference.Model != "small" || got.Failed {
			t.Fatalf("eviction order = %+v", want)
		}
	}
	step(t, ui, teasink.ConversationStartedMsg{TaskID: "root", Provider: "root-p", Model: "root-model"})
	step(t, ui, teasink.ConversationEndedMsg{TaskID: "root", Reason: runner.TerminalCompleted})
	step(t, ui, teasink.ConversationStartedMsg{TaskID: "next"})
	if got := ui.CompactionObservations(); !reflect.DeepEqual(got, want) {
		t.Fatalf("root start cleared session-wide evidence: %+v", got)
	}
	if got := ui.ProviderObservations(); len(got) != 0 {
		t.Fatalf("auxiliary evidence entered main diagnostics: %+v", got)
	}
}

func TestCompactionObservationsFullSessionReset(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reset func(*tui.UI)
	}{
		{name: "fresh", reset: func(ui *tui.UI) { ui.StartFreshSession("") }},
		{name: "clear", reset: func(ui *tui.UI) { ui.Submit("/clear") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ui := tui.New()
			step(t, ui, teasink.CompactionSettledMsg{CompactionSettled: runner.CompactionSettled{Inference: compactionEvidence()}})
			if len(ui.CompactionObservations()) != 1 {
				t.Fatal("manual evidence not recorded")
			}
			tc.reset(ui)
			if got := ui.CompactionObservations(); got != nil {
				t.Fatalf("full reset retained diagnostics: %+v", got)
			}
		})
	}
}

func TestCompactionObservationsOwnedSnapshotsAndPresence(t *testing.T) {
	for _, tc := range []struct {
		name      string
		inference compact.Inference
	}{
		{name: "absent"},
		{name: "unknown", inference: compact.Inference{Usage: &llm.Usage{}, PricingUsage: &llm.PricingUsage{}, Execution: &llm.ExecutionObservation{}}},
		{name: "zero and nested lifetimes", inference: compactionEvidence()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ui := tui.New()
			event := teasink.CompactionSettledMsg{CompactionSettled: runner.CompactionSettled{TaskID: "manual", Depth: 2, Inference: tc.inference, Err: context.Canceled}}
			want := []tui.CompactionDiagnostic{{TaskID: "manual", Depth: 2, Inference: tc.inference.Clone(), Failed: true}}
			step(t, ui, event)
			if got := ui.CompactionObservations(); !reflect.DeepEqual(got, want) {
				t.Fatalf("presence changed: got %+v, want %+v", got, want)
			}
			mutateCompactionEvidence(&event.Inference)
			snapshot := ui.CompactionObservations()
			snapshot[0].TaskID = "changed"
			snapshot[0].Failed = false
			mutateCompactionEvidence(&snapshot[0].Inference)
			if got := ui.CompactionObservations(); !reflect.DeepEqual(got, want) {
				t.Fatalf("retained aliases after event/snapshot mutation: %+v", got)
			}
		})
	}
}

func mutateCompactionEvidence(inference *compact.Inference) {
	inference.Provider, inference.Model = "changed", "changed"
	if inference.Usage != nil {
		inference.Usage.PromptTokens = 999
	}
	if inference.PricingUsage != nil {
		inference.PricingUsage.Input.CacheRead.Tokens = 999
		for i := range inference.PricingUsage.Input.WritesByLifetime {
			inference.PricingUsage.Input.WritesByLifetime[i].Quantity.Tokens = 999
			inference.PricingUsage.Input.WritesByLifetime[i].Lifetime = time.Second
		}
	}
	if inference.Execution != nil {
		inference.Execution.GatewayDeployment = "changed"
		inference.Execution.ReportedCost.Currency = "changed"
		inference.Execution.ReportedCost.Amount = 999
	}
}

func TestCompactionObservationsQueueToUpdateOwnership(t *testing.T) {
	messages := make(chan tea.Msg, 1)
	sink := teasink.New(func(msg tea.Msg) { messages <- msg })
	t.Cleanup(sink.Close)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	event := runner.CompactionSettled{TaskID: "manual", Depth: 3, Inference: compactionEvidence(), Err: context.Canceled}
	want := []tui.CompactionDiagnostic{{TaskID: "manual", Depth: 3, Inference: event.Inference.Clone(), Failed: true}}
	sink.OnCompactionSettled(ctx, event)
	// Publishing must already have released every caller-owned mutable alias.
	mutateCompactionEvidence(&event.Inference)
	var queued teasink.CompactionSettledMsg
	select {
	case msg := <-messages:
		var ok bool
		queued, ok = msg.(teasink.CompactionSettledMsg)
		if !ok {
			t.Fatalf("queued %T", msg)
		}
	case <-t.Context().Done():
		t.Fatal("settlement was not delivered")
	}
	sink.Drain()
	ui := tui.New()
	step(t, ui, queued)
	mutateCompactionEvidence(&queued.Inference)
	if got := ui.CompactionObservations(); !reflect.DeepEqual(got, want) {
		t.Fatalf("queue or Update retained borrowed sidecars: %+v", got)
	}
}

func TestCompactionObservationsPreserveLegacyAccountingAndMainDiagnostics(t *testing.T) {
	baseline, observed := tui.New(), tui.New()
	for _, ui := range []*tui.UI{baseline, observed} {
		ui.SetWorkspace(t.TempDir(), "root-model")
		ui.SetProvider("root-provider")
		ui.SetPricing(1, 2)
		ui.SetContextWindow(10000)
		step(t, ui, teasink.ConversationStartedMsg{TaskID: "root", Provider: "root-provider", Model: "root-model"})
		step(t, ui, teasink.ProviderAttemptSettledMsg{TaskID: "root", Attempt: 1, Execution: &llm.ExecutionObservation{ReportedModel: "root-served"}})
	}
	mainBefore, performanceBefore := observed.ProviderObservations(), observed.PerformanceDiagnostics()
	for _, ui := range []*tui.UI{baseline, observed} {
		inference := compactionEvidence()
		if ui == baseline {
			inference.PricingUsage, inference.Execution = nil, nil
		} else {
			inference.Execution.ReportedCost = llm.ReportedCost{Reported: true, Amount: 987654321, Currency: "EUR", CoverageKnown: true}
		}
		step(t, ui, teasink.CompactionSettledMsg{CompactionSettled: runner.CompactionSettled{TaskID: "root", Depth: 1, Inference: inference, Err: context.Canceled}})
	}
	performanceAfter := observed.PerformanceDiagnostics()
	// Live elapsed wall time advances between reads independently of settlement.
	performanceAfter.Duration = performanceBefore.Duration
	if !reflect.DeepEqual(observed.ProviderObservations(), mainBefore) || !reflect.DeepEqual(performanceAfter, performanceBefore) {
		t.Fatal("auxiliary settlement changed root or main diagnostics")
	}
	for _, ui := range []*tui.UI{baseline, observed} {
		step(t, ui, teasink.ConversationEndedMsg{TaskID: "root", Reason: runner.TerminalCompleted, TotalUsage: &llm.Usage{PromptTokens: 2000, CompletionTokens: 200}})
	}
	want := baseline.UsageSnapshot()
	if got := observed.UsageSnapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("sidecars changed legacy spend/cache accounting: got %+v, want %+v", got, want)
	}
	if want.CostUSD != 2.4 || want.In != 2100 || want.Out != 210 || want.Turns != 1 {
		t.Fatalf("legacy accounting = %+v", want)
	}
	if !reflect.DeepEqual(observed.CacheDiagnostics(), baseline.CacheDiagnostics()) {
		t.Fatal("sidecars changed cache target accounting")
	}
	beforeRender := observed.UsageSnapshot()
	_ = providerObservationDashboard(t, observed)
	if !reflect.DeepEqual(observed.UsageSnapshot(), beforeRender) {
		t.Fatal("render changed spend")
	}
}

func TestCompactionObservationsLeaveRootOccupancyUnchanged(t *testing.T) {
	ui := tui.New()
	ui.SetContextWindow(10000)
	step(t, ui, teasink.ConversationStartedMsg{TaskID: "root"})
	usage := &llm.Usage{PromptTokens: 8000, CachedTokens: 2000, CompletionTokens: 10}
	step(t, ui, teasink.IterationCompletedMsg{TaskID: "root", Iter: 1, Usage: usage, Delta: usage})
	_ = compactionDashboard(t, ui, 56)
	step(t, ui, tea.KeyPressMsg{Code: tea.KeyRight}) // The actual context dashboard tab.
	before := ui.View().Content
	inference := compactionEvidence()
	inference.Usage.PromptTokens = 999999
	step(t, ui, teasink.CompactionSettledMsg{CompactionSettled: runner.CompactionSettled{TaskID: "root", Inference: inference}})
	if after := ui.View().Content; after != before {
		t.Fatal("auxiliary settlement changed root occupancy/context view")
	}
}
