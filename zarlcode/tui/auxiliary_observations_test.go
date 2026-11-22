package tui_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func auxiliaryUIEvidence() engine.AuxiliaryObservation {
	return engine.AuxiliaryObservation{Kind: engine.AuxiliaryKinds.JUDGE, Inference: inference.Observation{TaskID: "child", Depth: 1, Provider: "actual-aux", Model: "aux-model", Duration: time.Second, Outcome: inference.StreamOutcomes.COMPLETED, Usage: &llm.Usage{PromptTokens: 999999}, PricingUsage: &llm.PricingUsage{Input: llm.PricingInputUsage{CacheRead: llm.TokenQuantity{Tokens: 30, Known: true}, WritesByLifetime: []llm.CacheWriteQuantity{{Lifetime: time.Hour, Quantity: llm.TokenQuantity{Known: true}}}}}, Execution: &llm.ExecutionObservation{GatewayDeployment: "SIDECAR_ONLY_CANARY", PromptCacheReadTokens: llm.TokenQuantity{Known: true}, ReportedCost: llm.ReportedCost{Amount: 0, Currency: "EUR", Reported: true, CoverageKnown: true}}}}
}

func TestAuxiliaryObservationsLatestLateChildResetAndOwnership(t *testing.T) {
	ui := tui.New()
	step(t, ui, teasink.ConversationStartedMsg{TaskID: "root"})
	step(t, ui, teasink.ConversationEndedMsg{TaskID: "root", Reason: runner.TerminalCompleted})
	// Root terminal is not turn drain. Do not reuse task accounting acceptance.
	o := auxiliaryUIEvidence()
	want := o.Clone()
	step(t, ui, teasink.AuxiliaryObservedMsg{Observation: o})
	o.Inference.Usage.PromptTokens = 1
	o.Inference.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 1
	o.Inference.Execution.GatewayDeployment = "mutated"
	snap := ui.AuxiliaryObservations()
	if !reflect.DeepEqual(snap.Judge, &want) || snap.SpawnPlanner != nil {
		t.Fatalf("snapshot=%+v", snap)
	}
	snap.Judge.Inference.Usage.PromptTokens = 1
	snap.Judge.Inference.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 1
	snap.Judge.Inference.Execution.ReportedCost.Amount = 1
	if !reflect.DeepEqual(ui.AuxiliaryObservations().Judge, &want) {
		t.Fatal("snapshot mutation escaped")
	}
	for i := range 4 {
		next := auxiliaryUIEvidence()
		next.Kind = engine.AuxiliaryKinds.SPAWNPLANNER
		next.Inference.Model = fmt.Sprintf("planner-%d", i)
		step(t, ui, teasink.AuxiliaryObservedMsg{Observation: next})
	}
	if got := ui.AuxiliaryObservations(); got.SpawnPlanner.Inference.Model != "planner-3" || !reflect.DeepEqual(got.Judge, &want) {
		t.Fatal("slots not independent latest-only")
	}
	step(t, ui, teasink.ConversationStartedMsg{TaskID: "new-root"})
	if got := ui.AuxiliaryObservations(); got.Judge != nil || got.SpawnPlanner != nil {
		t.Fatal("new root retained old evidence")
	}
	step(t, ui, teasink.AuxiliaryObservedMsg{Observation: want})
	ui.StartFreshSession("")
	if got := ui.AuxiliaryObservations(); got.Judge != nil || got.SpawnPlanner != nil {
		t.Fatal("reset retained evidence")
	}
}

func TestAuxiliaryObservationsAccountingDurableIsolationAndCompactorPolicy(t *testing.T) {
	baseline, observed := tui.New(), tui.New()
	for _, ui := range []*tui.UI{baseline, observed} {
		ui.SetPricing(1, 2)
		ui.SetContextWindow(10000)
		step(t, ui, teasink.ConversationStartedMsg{TaskID: "root"})
		usage := &llm.Usage{PromptTokens: 2000, CompletionTokens: 200}
		step(t, ui, teasink.IterationCompletedMsg{TaskID: "root", Iter: 1, Usage: usage, Delta: usage})
		step(t, ui, teasink.ConversationEndedMsg{TaskID: "root", Reason: runner.TerminalCompleted, TotalUsage: usage})
	}
	before := observed.UsageSnapshot()
	performance := observed.PerformanceDiagnostics()
	for range 5 {
		step(t, observed, teasink.AuxiliaryObservedMsg{Observation: auxiliaryUIEvidence()})
	}
	if !reflect.DeepEqual(observed.UsageSnapshot(), before) || !reflect.DeepEqual(observed.UsageSnapshot(), baseline.UsageSnapshot()) || !reflect.DeepEqual(observed.PerformanceDiagnostics(), performance) {
		t.Fatal("auxiliary update changed usage, USD or timing")
	}
	if len(observed.ProviderObservations()) != 0 || len(observed.CompactionObservations()) != 0 {
		t.Fatal("auxiliary roles entered another observation policy")
	}
	data, err := json.Marshal(observed.UsageSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "SIDECAR_ONLY_CANARY") || strings.Contains(string(data), "auxiliary") {
		t.Fatal("ephemeral diagnostics entered durable usage")
	}
	// Existing compactor retains newest three independently of these two slots.
	for range 4 {
		step(t, observed, teasink.CompactionSettledMsg{CompactionSettled: runner.CompactionSettled{Inference: compactionEvidence()}})
	}
	if len(observed.CompactionObservations()) != 3 || observed.AuxiliaryObservations().Judge == nil {
		t.Fatal("compactor newest-three policy changed")
	}
}

func TestAuxiliaryObservationsRenderLayoutsPresenceEscapingAndRevision(t *testing.T) {
	for _, width := range []int{56, 84, 168} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			ui := tui.New()
			step(t, ui, teasink.ConversationStartedMsg{TaskID: "root"})
			step(t, ui, teasink.ConversationEndedMsg{TaskID: "root", Reason: runner.TerminalCompleted})
			step(t, ui, tea.WindowSizeMsg{Width: width, Height: 260})
			step(t, ui, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'l'})
			before := ui.View().Content // Prime revision-backed rendering cache.
			o := auxiliaryUIEvidence()
			o.Inference.Provider = "p\n\x1b"
			step(t, ui, teasink.AuxiliaryObservedMsg{Observation: o})
			planner := engine.AuxiliaryObservation{Kind: engine.AuxiliaryKinds.SPAWNPLANNER, ResponseValid: true, Inference: inference.Observation{Outcome: inference.StreamOutcomes.FAILED}}
			step(t, ui, teasink.AuxiliaryObservedMsg{Observation: planner})
			view := ansi.Strip(ui.View().Content)
			if ui.View().Content == before {
				t.Fatal("accepted update did not invalidate rendered cache")
			}
			for _, want := range []string{"auxiliary judge/planner", "latest only", "judge · latest", "stream completed", "response invalid", "spawn_planner · latest", "stream failed", "response valid", "inference model \"aux-model\"", "reported cost 0", "prompt-cache read tokens 0", "prompt-cache write tokens unknown", "reported legacy tokens unknown", `p\n\x1b`} {
				if !strings.Contains(view, want) {
					t.Errorf("missing %q in:\n%s", want, view)
				}
			}
			if strings.Contains(view, "p\n") || strings.Contains(view, "success") {
				t.Fatal("unsafe controls or invalid response labeled success")
			}
			for line := range strings.SplitSeq(view, "\n") {
				if ansi.StringWidth(line) > width {
					t.Fatalf("line exceeds width %d: %q", width, line)
				}
			}
		})
	}
}

func TestAuxiliaryObservationsRestoreForkAndPersistedAllowlist(t *testing.T) {
	f, _ := newAccountingPersistenceFixture(t)
	settleRewindTurn(t, f, "same prompt")
	settleRewindTurn(t, f, "same prompt")
	id := f.ui.SessionIdentity()
	before := durableProviderAccounting(t, f, id)
	step(t, f.ui, teasink.AuxiliaryObservedMsg{Observation: auxiliaryUIEvidence()})
	if err := f.ui.SaveSession(t.Context()); err != nil {
		t.Fatal(err)
	}
	record, err := f.store.GetSession(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "SIDECAR_ONLY_CANARY") || strings.Contains(string(data), "auxiliary_observations") {
		t.Fatal("sidecar-only evidence leaked into session record")
	}
	if !reflect.DeepEqual(before, durableProviderAccounting(t, f, id)) {
		t.Fatal("ephemeral update changed PA1")
	}
	if err := f.ui.ResumeSavedSession(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if got := f.ui.AuxiliaryObservations(); got.Judge != nil || got.SpawnPlanner != nil {
		t.Fatal("restore retained auxiliary evidence")
	}
	step(t, f.ui, teasink.AuxiliaryObservedMsg{Observation: auxiliaryUIEvidence()})
	previewRewindPrompt(f, 1)
	_, apply := f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if f.ui.SessionIdentity() == id {
		t.Fatal("rewind did not fork", f.ui.ToastText())
	}
	if got := f.ui.AuxiliaryObservations(); got.Judge != nil || got.SpawnPlanner != nil {
		t.Fatal("fork retained evidence")
	}
	driveBeforeCommand(f, apply)
	if !reflect.DeepEqual(before, durableProviderAccounting(t, f, id)) {
		t.Fatal("fork changed source accounting")
	}
}
