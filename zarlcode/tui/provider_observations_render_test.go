package tui_test

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func providerObservationDashboard(t *testing.T, ui *tui.UI) string {
	t.Helper()
	return providerObservationDashboardAtWidth(t, ui, 56)
}

func providerObservationDashboardAtWidth(t *testing.T, ui *tui.UI, width int) string {
	t.Helper()
	_, _ = ui.Update(tea.WindowSizeMsg{Width: width, Height: 120})
	_, _ = ui.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'l'})
	return ansi.Strip(ui.View().Content)
}

func providerObservationSection(t *testing.T, view string) string {
	t.Helper()
	start := strings.Index(view, "provider observations")
	if start < 0 {
		t.Fatalf("missing provider observations in production dashboard:\n%s", view)
	}
	var lines []string
	for line := range strings.SplitSeq(view[start:], "\n") {
		if strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), "│")) == "context" {
			break
		}
		lines = append(lines, strings.TrimSpace(line))
	}
	return strings.Join(lines, "\n")
}

func TestProviderObservationsRenderPresenceAndIdentity(t *testing.T) {
	for _, tc := range []struct {
		name         string
		cost         llm.ReportedCost
		read         llm.TokenQuantity
		write        llm.TokenQuantity
		wantCost     string
		wantRead     string
		wantWrite    string
		wantCoverage string
	}{
		{name: "reported zero unknown currency", cost: llm.ReportedCost{Reported: true}, read: llm.TokenQuantity{Known: true}, wantCost: "reported cost 0 · currency unknown", wantRead: "0", wantWrite: "unknown", wantCoverage: "unknown"},
		{name: "absent cost known write zero", write: llm.TokenQuantity{Known: true}, wantCost: "reported cost unreported", wantRead: "unknown", wantWrite: "0", wantCoverage: "unknown"},
		{name: "explicit currency and coverage", cost: llm.ReportedCost{Reported: true, Amount: 2.5, Currency: "EUR", CoverageKnown: true}, read: llm.TokenQuantity{Known: true, Tokens: 17}, wantCost: `reported cost 2.5 · currency "EUR"`, wantRead: "17", wantWrite: "unknown", wantCoverage: "known"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ui := tui.New()
			_, _ = ui.Update(teasink.ConversationStartedMsg{TaskID: "root", Provider: "LiteLLM", Model: " Alias  V1 "})
			_, _ = ui.Update(teasink.ProviderAttemptSettledMsg{TaskID: "root", Attempt: 1, Execution: &llm.ExecutionObservation{
				GatewayDeployment: "placement-A", ReportedModel: "served-model", GatewayVersion: "1.2.3", ReportedVersion: "rev-4",
				ReportedCost: tc.cost, PromptCacheReadTokens: tc.read, PromptCacheWriteTokens: tc.write,
			}})
			view := providerObservationDashboard(t, ui)
			section := providerObservationSection(t, view)
			for _, want := range []string{
				`task "root" · depth 0 · attempt 1`, `requested provider "LiteLLM"`, `requested alias " Alias  V1 "`,
				`observed deployment "placement-A"`, `reported model "served-model"`, `gateway runtime version "1.2.3"`, `reported model version "rev-4"`,
				tc.wantCost, "reported cost coverage " + tc.wantCoverage,
				"prompt-cache read tokens " + tc.wantRead, "prompt-cache write tokens " + tc.wantWrite,
				"evidence, not session spend",
			} {
				if !strings.Contains(section, want) {
					t.Errorf("missing %q in:\n%s", want, section)
				}
			}
			if strings.ContainsAny(section, "$") || strings.Contains(section, "cache hit") || strings.Contains(section, "pinned") {
				t.Fatalf("observation inferred currency, cache hit or pin:\n%s", section)
			}
			if strings.Index(view, "performance") > strings.Index(view, "provider observations") {
				t.Fatal("provider observations precede performance")
			}
		})
	}
}

func TestProviderObservationsRenderRootChildRetriesAndBound(t *testing.T) {
	ui := tui.New()
	_, _ = ui.Update(teasink.ConversationStartedMsg{TaskID: "root", Provider: "root-p", Model: "root-alias"})
	_, _ = ui.Update(teasink.ConversationStartedMsg{TaskID: "child", Depth: 2, Provider: "child-p", Model: "child-alias"})
	for _, event := range []teasink.ProviderAttemptSettledMsg{
		{TaskID: "root", Attempt: 1}, {TaskID: "root", Attempt: 2}, {TaskID: "root", Attempt: 3},
		{TaskID: "child", Depth: 2, Attempt: 1}, {TaskID: "root", Attempt: 4},
	} {
		event.Execution = &llm.ExecutionObservation{ReportedCost: llm.ReportedCost{Reported: true}}
		_, _ = ui.Update(event)
	}
	section := providerObservationSection(t, providerObservationDashboard(t, ui))
	for _, want := range []string{
		"2 earlier observations omitted", `task "root" · depth 0 · attempt 3`, `task "child" · depth 2 · attempt 1`, `task "root" · depth 0 · attempt 4`,
		`requested provider "child-p"`, `requested alias "child-alias"`, `requested provider "root-p"`, `requested alias "root-alias"`,
	} {
		if !strings.Contains(section, want) {
			t.Errorf("missing %q in:\n%s", want, section)
		}
	}
	if strings.Count(section, "task ") != 3 || strings.Contains(section, `task "root" · depth 0 · attempt 2`) {
		t.Fatalf("settled row bound not respected:\n%s", section)
	}
	child := strings.Index(section, `task "child"`)
	if strings.Index(section, "attempt 3") > child || strings.Index(section, "attempt 4") < child {
		t.Fatal("settlement order changed")
	}
}

func TestProviderObservationsRenderSuppressesUnknownLegacyRows(t *testing.T) {
	for _, execution := range []*llm.ExecutionObservation{nil, {}} {
		ui := tui.New()
		_, _ = ui.Update(teasink.ConversationStartedMsg{TaskID: "root"})
		_, _ = ui.Update(teasink.ProviderAttemptSettledMsg{TaskID: "root", Attempt: 1, Execution: execution,
			Usage: &llm.Usage{CachedTokens: 400}, PricingUsage: &llm.PricingUsage{},
		})
		for _, width := range []int{56, 84, 168} {
			_, _ = ui.Update(tea.WindowSizeMsg{Width: width, Height: 120})
			// Open once, then resize the same dashboard.
			if width == 56 {
				_, _ = ui.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'l'})
			}
			if view := ansi.Strip(ui.View().Content); strings.Contains(view, "provider observations") {
				t.Fatalf("all-unknown/legacy evidence added an empty section at width %d:\n%s", width, view)
			}
		}
	}
}

func TestProviderObservationsRenderPricingCachePresence(t *testing.T) {
	ui := tui.New()
	_, _ = ui.Update(teasink.ConversationStartedMsg{TaskID: "root"})
	_, _ = ui.Update(teasink.ProviderAttemptSettledMsg{TaskID: "root", Attempt: 1,
		PricingUsage: &llm.PricingUsage{Input: llm.PricingInputUsage{CacheRead: llm.TokenQuantity{Known: true, Tokens: 21}, CacheWrite: llm.TokenQuantity{Known: true}}},
		Execution:    &llm.ExecutionObservation{PromptCacheReadTokens: llm.TokenQuantity{Known: true}},
	})
	section := providerObservationSection(t, providerObservationDashboard(t, ui))
	for _, want := range []string{"prompt-cache read tokens 0", "prompt-cache write tokens 0", "reported cost unreported"} {
		if !strings.Contains(section, want) {
			t.Errorf("missing %q in:\n%s", want, section)
		}
	}
}

func TestProviderObservationsRenderEscapesHostileIDsAndFits(t *testing.T) {
	for _, hostile := range []string{"x\ny", "x\ry", "x\ty", "x\x1b[2Jy", "x\u009by", "x\u202ey", "x\"\\y"} {
		t.Run(fmt.Sprintf("%q", hostile), func(t *testing.T) {
			ui := tui.New()
			_, _ = ui.Update(teasink.ConversationStartedMsg{TaskID: hostile, Provider: hostile, Model: hostile})
			_, _ = ui.Update(teasink.ProviderAttemptSettledMsg{TaskID: hostile, Attempt: 1,
				RateCard: &llm.RateCard{Reference: llm.RateCardReference{ID: hostile, Revision: hostile, Entry: hostile}, PricingProvider: llm.PricingProviderID(hostile), Currency: llm.Currency(hostile)},
				Execution: &llm.ExecutionObservation{
					GatewayDeployment: llm.DeploymentID(hostile), ReportedModel: llm.ModelID(hostile), GatewayVersion: llm.GatewayVersionID(hostile),
					ReportedVersion: llm.ModelVersionID(hostile), ReportedCost: llm.ReportedCost{Reported: true, Currency: llm.Currency(hostile)},
				}})
			section := providerObservationSection(t, providerObservationDashboard(t, ui))
			if strings.Contains(section, hostile) {
				t.Fatalf("unescaped ID survived: %q", section)
			}
			for _, want := range []string{"task ", "requested provider ", "requested alias ", "observed deployment ", "reported model ", "gateway runtime version ", "reported model version ", "currency "} {
				if !strings.Contains(section, want+fmt.Sprintf("%q", hostile)) {
					t.Errorf("missing quoted ID for %q in:\n%s", want, section)
				}
			}
			for _, width := range []int{36, 48, 56, 84, 168} {
				_, _ = ui.Update(tea.WindowSizeMsg{Width: width, Height: 120})
				raw := ui.View().Content
				if strings.Contains(raw, "\x1b[2J") || strings.ContainsAny(ansi.Strip(raw), "\r\t\u009b\u202e") {
					t.Fatalf("terminal control injection at width %d", width)
				}
				for line := range strings.SplitSeq(raw, "\n") {
					if got := ansi.StringWidth(line); got > width {
						t.Fatalf("view line exceeds %d columns: %d: %q", width, got, line)
					}
				}
			}
		})
	}
}

func TestProviderObservationsRenderLeavesLegacySpendUnchanged(t *testing.T) {
	ui := tui.New()
	ui.SetPricing(1, 2)
	_, _ = ui.Update(teasink.ConversationStartedMsg{TaskID: "root"})
	_, _ = ui.Update(teasink.ProviderAttemptSettledMsg{TaskID: "root", Attempt: 1,
		Execution: &llm.ExecutionObservation{ReportedCost: llm.ReportedCost{Reported: true, Amount: 9876, Currency: "EUR"}},
	})
	if got := ui.UsageSnapshot(); got.CostUSD != 0 {
		t.Fatalf("observation booked spend: %+v", got)
	}
	_, _ = ui.Update(teasink.ConversationEndedMsg{TaskID: "root", Reason: runner.TerminalCompleted,
		TotalUsage: &llm.Usage{PromptTokens: 2000, CompletionTokens: 200},
	})
	before := ui.UsageSnapshot()
	view := providerObservationDashboard(t, ui)
	if before.CostUSD != 2.4 || before.In != 2000 || before.Out != 200 || before.Turns != 1 {
		t.Fatalf("legacy settlement changed: %+v", before)
	}
	if after := ui.UsageSnapshot(); after.CostUSD != before.CostUSD || after.In != before.In || after.Out != before.Out || after.Turns != before.Turns {
		t.Fatalf("render changed accounting: before %+v, after %+v", before, after)
	}
	section := providerObservationSection(t, view)
	if !strings.Contains(section, `reported cost 9876 · currency "EUR"`) || strings.Contains(section, "$") {
		t.Fatalf("reported cost merged with legacy USD spend:\n%s", section)
	}
}

func TestProviderObservationsRenderDashboardColumns(t *testing.T) {
	for _, tc := range []struct {
		name  string
		width int
	}{
		{name: "one column", width: 56},
		{name: "two columns", width: 84},
		{name: "three columns", width: 168},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ui := tui.New()
			ui.SetPricing(1, 2)
			_, _ = ui.Update(teasink.ConversationStartedMsg{TaskID: "root", Provider: "LiteLLM", Model: "alias"})
			for attempt := 1; attempt <= 5; attempt++ {
				_, _ = ui.Update(teasink.ProviderAttemptSettledMsg{TaskID: "root", Attempt: attempt,
					Execution: &llm.ExecutionObservation{
						GatewayDeployment: "deployment", ReportedModel: "served", GatewayVersion: "1.2.3",
						ReportedCost: llm.ReportedCost{Reported: true}, PromptCacheReadTokens: llm.TokenQuantity{Known: true},
					},
				})
			}
			if got := ui.UsageSnapshot(); got.CostUSD != 0 {
				t.Fatalf("provider observations booked spend: %+v", got)
			}
			_, _ = ui.Update(teasink.ConversationEndedMsg{TaskID: "root", Reason: runner.TerminalCompleted,
				TotalUsage: &llm.Usage{PromptTokens: 2000, CompletionTokens: 200},
			})
			before := ui.UsageSnapshot()
			view := providerObservationDashboardAtWidth(t, ui, tc.width)
			for _, want := range []string{
				"provider observations", "settled attempts", "2 earlier observations omitted",
				`task "root" · depth 0 · attempt 3`, `task "root" · depth 0 · attempt 4`, `task "root" · depth 0 · attempt 5`,
				`requested provider "LiteLLM"`, `requested alias "alias"`, `observed deployment "deployment"`,
				`reported model "served"`, `gateway runtime version "1.2.3"`,
				"reported cost 0 · currency unknown", "reported cost coverage unknown",
				"prompt-cache read tokens 0", "prompt-cache write tokens unknown",
				"base-rate estimate", "$2.40",
			} {
				if !strings.Contains(view, want) {
					t.Errorf("missing %q in:\n%s", want, view)
				}
			}
			if strings.Count(view, "provider observations") != 1 || strings.Count(view, `task "root" · depth 0 · attempt`) != 3 {
				t.Fatalf("duplicate section or unbounded attempts:\n%s", view)
			}
			after := ui.UsageSnapshot()
			if before.CostUSD != 2.4 || after.CostUSD != before.CostUSD || after.In != before.In || after.Out != before.Out || after.Turns != before.Turns {
				t.Fatalf("observations or rendering changed legacy spend: before %+v, after %+v", before, after)
			}
			if tc.width > 56 {
				observationX, signalsX := -1, -1
				for line := range strings.SplitSeq(view, "\n") {
					if x := strings.Index(line, "provider observations"); x >= 0 {
						observationX = x
					}
					if x := strings.Index(line, "signals"); x >= 0 {
						signalsX = x
					}
				}
				if observationX < 0 || signalsX < 0 || (tc.width == 84 && observationX >= signalsX) || (tc.width == 168 && observationX != signalsX) {
					t.Fatalf("unexpected %s layout: observations x=%d, signals x=%d\n%s", tc.name, observationX, signalsX, view)
				}
			}
			// Long identifiers stay within their column rather than wrapping into
			// extra diagnostic rows or widening the public terminal view.
			longID := strings.Repeat("long-model-", 20)
			_, _ = ui.Update(teasink.ConversationStartedMsg{TaskID: "long"})
			_, _ = ui.Update(teasink.ProviderAttemptSettledMsg{TaskID: "long", Attempt: 1,
				Execution: &llm.ExecutionObservation{ReportedModel: llm.ModelID(longID)},
			})
			raw := ui.View().Content
			if strings.Contains(ansi.Strip(raw), longID) || !strings.Contains(ansi.Strip(raw), "reported model \"long-model-") || !strings.Contains(ansi.Strip(raw), "…") {
				t.Fatalf("long observation was not truncated:\n%s", ansi.Strip(raw))
			}
			for line := range strings.SplitSeq(raw, "\n") {
				if got := ansi.StringWidth(line); got > tc.width {
					t.Fatalf("view line exceeds %d columns: %d: %q", tc.width, got, line)
				}
			}
		})
	}
}

func TestProviderObservationsRenderConfiguredEstimateCoverage(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		missing, unknown, zero, extra bool
		currency, subtotal, coverage  string
	}{
		{name: "EUR independent of reported currency", currency: "EUR", subtotal: "0.0034", coverage: "complete (configured tokens)"},
		{name: "known zero", currency: "credits", zero: true, subtotal: "0", coverage: "complete (configured tokens)"},
		{name: "unknown quantities", currency: "EUR", unknown: true, subtotal: "unknown", coverage: "incomplete"},
		{name: "additional charges", currency: "EUR", extra: true, subtotal: "0.0034", coverage: "incomplete"},
		{name: "missing normalized data", currency: "EUR", missing: true, coverage: "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			card := providerConfiguredCard(t, "render-r1", tc.currency)
			if tc.zero {
				card.FreshInput.PerMillionTokens = 0
				card.CacheRead.PerMillionTokens = 0
				card.Output.PerMillionTokens = 0
				for i := range card.CacheWrites {
					card.CacheWrites[i].Rate.PerMillionTokens = 0
				}
			}
			usage := providerConfiguredUsage()
			if tc.unknown {
				usage = llm.PricingUsage{}
			}
			usage.AdditionalChargesPossible = tc.extra
			event := teasink.ProviderAttemptSettledMsg{TaskID: "root", Attempt: 1, RateCard: &card, PricingUsage: &usage,
				Usage:     &llm.Usage{PromptTokens: 1000, CompletionTokens: 100},
				Execution: &llm.ExecutionObservation{ReportedCost: llm.ReportedCost{Reported: true, Amount: 500}},
			}
			if tc.missing {
				event.PricingUsage = nil
			}
			ui := tui.New()
			_, _ = ui.Update(teasink.ConversationStartedMsg{TaskID: "root"})
			_, _ = ui.Update(event)
			section := providerObservationSection(t, providerObservationDashboard(t, ui))
			for _, want := range []string{"configured estimate", "not reported cost/invoice", `basis "policy"`, `basis revision "render-r1"`, `basis entry "exact-entry"`, `pricing provider "operator"`, `estimate currency "` + tc.currency + `"`, "estimate coverage " + tc.coverage, "reported cost 500 · currency unknown"} {
				if !strings.Contains(section, want) {
					t.Errorf("missing %q in:\n%s", want, section)
				}
			}
			if tc.missing {
				if !strings.Contains(section, "estimate unavailable: missing normalized usage") || strings.Contains(section, "configured subtotal") {
					t.Fatalf("missing usage became numeric estimate:\n%s", section)
				}
			} else if !strings.Contains(section, "configured subtotal "+tc.subtotal) {
				t.Fatalf("subtotal missing:\n%s", section)
			}
			if strings.Contains(section, "$") || ui.UsageSnapshot().CostUSD != 0 {
				t.Fatal("configured/report evidence became USD spend")
			}
		})
	}
}

func TestProviderObservationsRenderPricedOnlyAttemptsRespectBound(t *testing.T) {
	ui := tui.New()
	_, _ = ui.Update(teasink.ConversationStartedMsg{TaskID: "root"})
	for i, currency := range []string{"EUR", "credits", "USD", "EUR"} {
		card := providerConfiguredCard(t, fmt.Sprintf("r%d", i+1), currency)
		usage := providerConfiguredUsage()
		_, _ = ui.Update(teasink.ProviderAttemptSettledMsg{TaskID: "root", Attempt: i + 1, RateCard: &card, PricingUsage: &usage})
	}
	section := providerObservationSection(t, providerObservationDashboard(t, ui))
	for _, want := range []string{"1 earlier observations omitted", `basis revision "r2"`, `basis revision "r3"`, `basis revision "r4"`, `estimate currency "credits"`, `estimate currency "USD"`, `estimate currency "EUR"`} {
		if !strings.Contains(section, want) {
			t.Errorf("missing %q in:\n%s", want, section)
		}
	}
	if strings.Count(section, "configured estimate") != 3 || strings.Count(section, "task ") != 3 || strings.Contains(section, `basis revision "r1"`) || strings.Contains(section, "$") {
		t.Fatalf("priced-only rows hidden, unbounded or converted:\n%s", section)
	}
}
