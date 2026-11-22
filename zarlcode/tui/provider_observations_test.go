package tui_test

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestProviderObservationsOwnSnapshots(t *testing.T) {
	ui := tui.New()
	_, _ = ui.Update(teasink.ConversationStartedMsg{TaskID: "root", Provider: "litellm", Model: "alias"})
	usage := &llm.Usage{PromptTokens: 20}
	pricing := &llm.PricingUsage{Input: llm.PricingInputUsage{
		WritesByLifetime: []llm.CacheWriteQuantity{{Quantity: llm.TokenQuantity{Known: true, Tokens: 4}}},
	}}
	execution := &llm.ExecutionObservation{GatewayDeployment: "deployment-a", ReportedCost: llm.ReportedCost{Reported: true}}
	card := providerConfiguredCard(t, "owned-r1", "EUR")
	event := teasink.ProviderAttemptSettledMsg{TaskID: "root", Attempt: 1, Usage: usage, PricingUsage: pricing, Execution: execution, RateCard: &card}
	_, _ = ui.Update(event)
	want := ui.ProviderObservations()
	if len(want) != 1 || want[0].Provider != "litellm" || want[0].Model != "alias" {
		t.Fatalf("diagnostics = %+v", want)
	}
	card.Reference.Revision = "source-mutated"
	card.CacheWrites[0].Rate.PerMillionTokens = 900
	usage.PromptTokens = 900
	pricing.Input.WritesByLifetime[0].Quantity.Tokens = 900
	execution.GatewayDeployment = "borrowed-mutated"
	execution.ReportedCost.Currency = "USD"
	if got := ui.ProviderObservations(); !reflect.DeepEqual(got, want) {
		t.Fatalf("retained event aliases caller: got %+v, want %+v", got, want)
	}
	snapshot := ui.ProviderObservations()
	snapshot[0].RateCard.Reference.Revision = "snapshot-mutated"
	snapshot[0].RateCard.CacheWrites[0].Rate.PerMillionTokens = 800
	snapshot[0].ConfiguredEstimate.Reference.Revision = "snapshot-mutated"
	snapshot[0].ConfiguredEstimate.Issues[0].Field = "snapshot-mutated"
	snapshot[0].TaskID = "snapshot-mutated"
	snapshot[0].Usage.PromptTokens = 800
	snapshot[0].PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 800
	snapshot[0].Execution.GatewayDeployment = "snapshot-mutated"
	snapshot[0].Execution.ReportedCost.Currency = "EUR"
	if got := ui.ProviderObservations(); !reflect.DeepEqual(got, want) {
		t.Fatal("snapshot mutation changed retained diagnostics")
	}
	if got := ui.ProviderObservations()[0].Execution.ReportedCost; !got.Reported || got.Amount != 0 || got.Currency != "" || got.CoverageKnown {
		t.Fatalf("reported zero/unknown currency changed: %+v", got)
	}
}

func TestProviderObservationsPreservePresence(t *testing.T) {
	for _, tc := range []struct {
		name      string
		pricing   *llm.PricingUsage
		execution *llm.ExecutionObservation
	}{
		{name: "absent"},
		{name: "present unknown", pricing: &llm.PricingUsage{}, execution: &llm.ExecutionObservation{}},
		{name: "known zero", pricing: &llm.PricingUsage{Input: llm.PricingInputUsage{Total: llm.TokenQuantity{Known: true}}}, execution: &llm.ExecutionObservation{PromptCacheReadTokens: llm.TokenQuantity{Known: true}, ReportedCost: llm.ReportedCost{Reported: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ui := tui.New()
			_, _ = ui.Update(teasink.ConversationStartedMsg{TaskID: "root"})
			_, _ = ui.Update(teasink.ProviderAttemptSettledMsg{TaskID: "root", Attempt: 1, PricingUsage: tc.pricing, Execution: tc.execution})
			got := ui.ProviderObservations()
			if len(got) != 1 || got[0].Usage != nil || got[0].RateCard != nil || got[0].ConfiguredEstimate != nil || !reflect.DeepEqual(got[0].PricingUsage, tc.pricing) || !reflect.DeepEqual(got[0].Execution, tc.execution) {
				t.Fatalf("presence changed: %+v", got)
			}
		})
	}
}

func TestProviderObservationsTaskAcceptanceAndReset(t *testing.T) {
	ui := tui.New()
	ui.SetWorkspace(t.TempDir(), "alias")
	ui.SetProvider("configured")
	ui.SetPricing(1, 2)
	attempt := func(task string, depth, number int, placement llm.DeploymentID) {
		t.Helper()
		_, _ = ui.Update(teasink.ProviderAttemptSettledMsg{TaskID: task, Depth: depth, Attempt: number,
			Usage:     &llm.Usage{PromptTokens: 1000, CompletionTokens: 100},
			Execution: &llm.ExecutionObservation{GatewayDeployment: placement, ReportedCost: llm.ReportedCost{Reported: true, Amount: 1234}},
		})
	}
	attempt("unknown", 0, 1, "ignored")
	_, _ = ui.Update(teasink.ConversationStartedMsg{TaskID: "orphan", Depth: 1})
	attempt("orphan", 1, 1, "ignored")
	if len(ui.ProviderObservations()) != 0 {
		t.Fatal("out-of-turn observation accepted")
	}
	_, _ = ui.Update(teasink.ConversationEndedMsg{TaskID: "orphan", Depth: 1})
	_, _ = ui.Update(teasink.ConversationStartedMsg{TaskID: "root", Provider: "configured", Model: "alias"})
	attempt("root", 1, 10, "wrong-depth")
	attempt("root", 0, 0, "invalid-attempt")
	attempt("root", 0, 1, "a")
	_, _ = ui.Update(teasink.ConversationStartedMsg{TaskID: "child", Depth: 1, Provider: "configured", Model: "alias"})
	attempt("child", 1, 1, "child-a")
	attempt("root", 0, 2, "b")
	_, _ = ui.Update(teasink.ConversationStartedMsg{TaskID: "root", Provider: "duplicate", Model: "duplicate"})
	want := ui.ProviderObservations()
	if len(want) != 3 || want[0].TaskID != "root" || want[1].TaskID != "child" || want[1].Depth != 1 || want[2].Attempt != 2 || want[2].Execution.GatewayDeployment != "b" {
		t.Fatalf("interleaved observations = %+v", want)
	}
	for _, d := range want {
		if d.Provider != "configured" || d.Model != "alias" {
			t.Fatalf("task-start attribution = %+v", d)
		}
	}
	attempt("root", 0, 2, "duplicate")
	attempt("root", 0, 1, "older")
	attempt("child", 2, 2, "wrong-depth")
	attempt("unknown", 0, 10, "unaccepted")
	if !reflect.DeepEqual(ui.ProviderObservations(), want) {
		t.Fatal("duplicate/stale/depth event changed diagnostics")
	}
	if ui.UsageSnapshot().In != 0 || ui.UsageSnapshot().CostUSD != 0 {
		t.Fatal("attempts booked spend")
	}
	total := &llm.Usage{PromptTokens: 2000, CompletionTokens: 200}
	for _, end := range []teasink.ConversationEndedMsg{
		{TaskID: "child", Depth: 1, TotalUsage: total, Reason: runner.TerminalError},
		{TaskID: "root", TotalUsage: total, Reason: runner.TerminalCompleted},
	} {
		_, _ = ui.Update(end)
		_, _ = ui.Update(end)
		attempt(end.TaskID, end.Depth, 99, "completed-task")
	}
	if !reflect.DeepEqual(ui.ProviderObservations(), want) {
		t.Fatal("completed-task event changed retained diagnostics")
	}
	usage := ui.UsageSnapshot()
	if usage.In != 4000 || usage.Out != 400 || usage.Turns != 1 || usage.CostUSD != 4.8 {
		t.Fatalf("legacy spend changed: %+v", usage)
	}
	_, _ = ui.Update(teasink.ConversationStartedMsg{TaskID: "next", Provider: "new", Model: "new-alias"})
	if len(ui.ProviderObservations()) != 0 {
		t.Fatal("root start retained previous-turn diagnostics")
	}
	attempt("root", 0, 100, "stale-root")
	attempt("child", 1, 100, "stale-child")
	if len(ui.ProviderObservations()) != 0 {
		t.Fatal("stale task mutated next turn")
	}
	attempt("next", 0, 1, "next-placement")
	got := ui.ProviderObservations()
	if len(got) != 1 || got[0].TaskID != "next" || got[0].Provider != "new" {
		t.Fatalf("next turn = %+v", got)
	}
	if !reflect.DeepEqual(ui.UsageSnapshot(), usage) {
		t.Fatal("diagnostic reset changed spend")
	}
}

func TestProviderObservationsExcludeTasksFromEarlierRoot(t *testing.T) {
	ui := tui.New()
	_, _ = ui.Update(teasink.ConversationStartedMsg{TaskID: "old-root"})
	_, _ = ui.Update(teasink.ConversationStartedMsg{TaskID: "old-child", Depth: 1})
	_, _ = ui.Update(teasink.ConversationStartedMsg{TaskID: "new-root"})
	_, _ = ui.Update(teasink.ConversationStartedMsg{TaskID: "new-child", Depth: 1})
	for _, event := range []teasink.ProviderAttemptSettledMsg{
		{TaskID: "old-root", Attempt: 1},
		{TaskID: "old-child", Depth: 1, Attempt: 1},
		{TaskID: "new-child", Depth: 1, Attempt: 1},
		{TaskID: "new-root", Attempt: 1},
	} {
		_, _ = ui.Update(event)
	}
	got := ui.ProviderObservations()
	if len(got) != 2 || got[0].TaskID != "new-child" || got[1].TaskID != "new-root" {
		t.Fatalf("previous-turn tasks entered latest diagnostics: %+v", got)
	}
}

func providerConfiguredCard(t *testing.T, revision, currency string) llm.RateCard {
	t.Helper()
	config, err := engine.ParseRateCardConfig([]byte(fmt.Sprintf(`{"version":1,"target":{"provider":"gateway","model":"Team/Alias","base_url":"","protocol":"chat_completions"},"card":{
 "id":"policy","revision":%q,"entry":"exact-entry","pricing_provider":"operator","currency":%q,"billing":"token-metered","applicable":true,"token_only":true,
 "fresh_input_per_million":2,"cache_read_per_million":0.5,"output_per_million":3,
 "cache_writes":[{"lifetime_seconds":300,"per_million":4},{"lifetime_seconds":3600,"per_million":6}]}}`, revision, currency)), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	card, err := config.Select(engine.RateCardTarget{Provider: "gateway", Model: "Team/Alias", Protocol: llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS})
	if err != nil {
		t.Fatal(err)
	}
	return card
}

func providerConfiguredUsage() llm.PricingUsage {
	q := func(n int64) llm.TokenQuantity { return llm.TokenQuantity{Known: true, Tokens: n} }
	return llm.PricingUsage{Input: llm.PricingInputUsage{
		Total: q(1000), CacheRead: q(200), CacheWrite: q(400), Other: q(0), PartitionKnown: true,
		WritesByLifetime: []llm.CacheWriteQuantity{{Lifetime: 5 * time.Minute, Quantity: q(100)}, {Lifetime: time.Hour, Quantity: q(300)}},
	}, Output: llm.PricingOutputUsage{Total: q(100), Other: q(0)}}
}

type pricedObservationClient struct{ pricing llm.PricingUsage }

func (c *pricedObservationClient) Complete(context.Context, llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		yield(llm.CompletionChunk{Content: "answer", UsageReported: true, Usage: llm.Usage{PromptTokens: 1000, CompletionTokens: 100}, PricingUsage: &c.pricing,
			Execution: &llm.ExecutionObservation{ReportedCost: llm.ReportedCost{Amount: 999, Reported: true}},
		}, nil)
		c.pricing.Input.WritesByLifetime[0].Quantity.Tokens = 999
	}
}

func TestProviderObservationsConfiguredParseRunnerSinkUI(t *testing.T) {
	card := providerConfiguredCard(t, "original", "EUR")
	var mu sync.Mutex
	var messages []tea.Msg
	sink := teasink.New(func(msg tea.Msg) { mu.Lock(); defer mu.Unlock(); messages = append(messages, msg) })
	t.Cleanup(sink.Close)
	client := &pricedObservationClient{pricing: providerConfiguredUsage()}
	r := runner.New(client, runner.WithRateCard(card), runner.WithSink(sink), runner.WithModelIdentity("gateway", "Team/Alias"))
	card.Reference.Revision = "source-mutated"
	card.CacheWrites[0].Rate.PerMillionTokens = 999
	result := r.Run(t.Context(), runner.TaskSpec{ID: "root", Prompt: "hello"})
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	sink.Drain()
	mu.Lock()
	delivered := append([]tea.Msg(nil), messages...)
	mu.Unlock()
	// Mutating retained runner evidence cannot change the queued sink snapshot.
	result.ProviderObservations[0].RateCard.CacheWrites[0].Rate.PerMillionTokens = 888
	result.ProviderObservations[0].PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 888
	ui := tui.New()
	ui.SetPricing(1, 2)
	for _, msg := range delivered {
		_, _ = ui.Update(msg)
	}
	got := ui.ProviderObservations()
	if len(got) != 1 || got[0].RateCard.Reference.Revision != "original" || got[0].RateCard.CacheWrites[0].Rate.PerMillionTokens != 4 || got[0].PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens != 100 {
		t.Fatalf("diagnostics = %+v", got)
	}
	e := got[0].ConfiguredEstimate
	if e == nil || !e.Complete || !e.HasKnownSubtotal || math.Abs(e.Subtotal-0.0034) > 1e-12 || e.Currency != "EUR" || got[0].Execution.ReportedCost.Currency != "" || got[0].Execution.ReportedCost.Amount != 999 {
		t.Fatalf("configured estimate=%+v, reported=%+v", e, got[0].Execution)
	}
	if spend := ui.UsageSnapshot(); spend.CostUSD != 1.2 || spend.In != 1000 || spend.Out != 100 || spend.Turns != 1 {
		t.Fatalf("new diagnostics changed legacy spend: %+v", spend)
	}
	for _, msg := range delivered {
		if attempt, ok := msg.(teasink.ProviderAttemptSettledMsg); ok {
			attempt.RateCard.Reference.Revision = "queued-message-mutated"
			attempt.RateCard.CacheWrites[0].Rate.PerMillionTokens = 777
		}
	}
	got[0].RateCard.CacheWrites[0].Rate.PerMillionTokens = 666
	got[0].ConfiguredEstimate.Subtotal = 666
	fresh := ui.ProviderObservations()[0]
	if fresh.RateCard.Reference.Revision != "original" || fresh.RateCard.CacheWrites[0].Rate.PerMillionTokens != 4 || math.Abs(fresh.ConfiguredEstimate.Subtotal-0.0034) > 1e-12 {
		t.Fatal("queued source or diagnostic snapshot repriced retained attempt")
	}
}

func TestProviderObservationsConfiguredBasisRevisionsRemainIndependent(t *testing.T) {
	ui := tui.New()
	ui.SetPricing(1, 2)
	_, _ = ui.Update(teasink.ConversationStartedMsg{TaskID: "root"})
	for i, currency := range []string{"EUR", "credits", "USD"} {
		card := providerConfiguredCard(t, fmt.Sprintf("r%d", i+1), currency)
		usage := providerConfiguredUsage()
		_, _ = ui.Update(teasink.ProviderAttemptSettledMsg{TaskID: "root", Attempt: i + 1, RateCard: &card, PricingUsage: &usage})
		card.Reference.Revision = "later"
		card.CacheWrites[0].Rate.PerMillionTokens = 999
	}
	got := ui.ProviderObservations()
	for i, currency := range []string{"EUR", "credits", "USD"} {
		if got[i].RateCard.Reference.Revision != fmt.Sprintf("r%d", i+1) || got[i].ConfiguredEstimate.Currency != llm.Currency(currency) || math.Abs(got[i].ConfiguredEstimate.Subtotal-0.0034) > 1e-12 {
			t.Fatalf("attempt %d repriced: %+v", i+1, got[i])
		}
	}
	if ui.UsageSnapshot().CostUSD != 0 {
		t.Fatal("attempts booked estimates")
	}
	_, _ = ui.Update(teasink.ConversationEndedMsg{TaskID: "root", Reason: runner.TerminalCompleted, TotalUsage: &llm.Usage{PromptTokens: 1000, CompletionTokens: 100}})
	if ui.UsageSnapshot().CostUSD != 1.2 {
		t.Fatal("terminal booked estimates again")
	}
}
