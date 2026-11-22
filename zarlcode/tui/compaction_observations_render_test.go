package tui_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func compactionDashboard(t *testing.T, ui *tui.UI, width int) string {
	t.Helper()
	step(t, ui, tea.WindowSizeMsg{Width: width, Height: 240})
	step(t, ui, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'l'})
	return ansi.Strip(ui.View().Content)
}

func TestCompactionObservationsRenderPresence(t *testing.T) {
	for _, tc := range []struct {
		name      string
		inference compact.Inference
		want      []string
	}{
		{name: "absent", want: []string{"reported cost unreported", "reported cost coverage unknown", "prompt-cache read tokens unknown", "prompt-cache write tokens unknown"}},
		{name: "present unknown", inference: compact.Inference{PricingUsage: &llm.PricingUsage{}, Execution: &llm.ExecutionObservation{}}, want: []string{"reported cost unreported", "prompt-cache read tokens unknown", "prompt-cache write tokens unknown"}},
		{name: "known zero overrides pricing", inference: compactionEvidence(), want: []string{"reported cost 0 · currency unknown", "reported cost coverage unknown", "prompt-cache read tokens 0", "prompt-cache write tokens 30"}},
		{name: "explicit currency coverage and write zero", inference: compact.Inference{Execution: &llm.ExecutionObservation{
			ReportedCost: llm.ReportedCost{Reported: true, Amount: 2.5, Currency: "EUR", CoverageKnown: true}, PromptCacheWriteTokens: llm.TokenQuantity{Known: true},
		}}, want: []string{`reported cost 2.5 · currency "EUR"`, "reported cost coverage known", "prompt-cache read tokens unknown", "prompt-cache write tokens 0"}},
		{name: "legacy cache zero is not explicit", inference: compact.Inference{Usage: &llm.Usage{CachedTokens: 20, CacheCreationTokens: 30}}, want: []string{"prompt-cache read tokens unknown", "prompt-cache write tokens unknown"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ui := tui.New()
			tc.inference.Provider, tc.inference.Model = "actual-aux", "actual-model"
			step(t, ui, teasink.CompactionSettledMsg{CompactionSettled: runner.CompactionSettled{TaskID: "manual", Depth: 2, Inference: tc.inference, Err: context.Canceled}})
			view := compactionDashboard(t, ui, 56)
			for _, want := range append(tc.want, "auxiliary compaction · evidence, not session spend", `task "manual" · depth 2 · failed`, `inference provider "actual-aux"`, `inference model "actual-model"`) {
				if !strings.Contains(view, want) {
					t.Errorf("missing %q in:\n%s", want, view)
				}
			}
			start := strings.Index(view, "auxiliary compaction")
			end := strings.Index(view[start:], "context")
			section := view[start : start+end]
			for _, forbidden := range []string{"attempt ", "configured estimate", "basis ", "$", "requested alias", "cache hit", "pinned"} {
				if strings.Contains(section, forbidden) {
					t.Fatalf("invented task/price/placement semantics %q:\n%s", forbidden, section)
				}
			}
		})
	}
}

func TestCompactionObservationsRenderAllDashboardLayouts(t *testing.T) {
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
			for i := range 4 {
				step(t, ui, teasink.CompactionSettledMsg{CompactionSettled: runner.CompactionSettled{TaskID: taskscope.ID(fmt.Sprintf("aux-%d", i)), Inference: compactionEvidence()}})
			}
			step(t, ui, teasink.ConversationStartedMsg{TaskID: "root", Provider: "root-p", Model: "root-model"})
			step(t, ui, teasink.ProviderAttemptSettledMsg{TaskID: "root", Attempt: 1, Execution: &llm.ExecutionObservation{ReportedModel: "main-only"}})
			step(t, ui, teasink.ConversationEndedMsg{TaskID: "root", Reason: runner.TerminalCompleted})
			beforeUsage, beforeMain := ui.UsageSnapshot(), ui.ProviderObservations()
			view := compactionDashboard(t, ui, tc.width)
			for _, want := range []string{
				"auxiliary compaction", "evidence, not", "spend",
				`task "aux-1" · depth 0 · settled`, `task "aux-2" · depth 0 · settled`, `task "aux-3" · depth 0 · settled`,
				`inference provider "aux-provider"`, `inference model "aux-model"`, `observed deployment "placement"`,
				`reported model "served"`, `gateway runtime version "gw-v1"`, `reported model version "model-v2"`,
				"reported cost 0 · currency unknown", "prompt-cache read tokens 0", "prompt-cache write tokens 30",
				"provider observations", `task "root" · depth 0 · attempt 1`, `reported model "main-only"`,
			} {
				if !strings.Contains(view, want) {
					t.Errorf("missing %q in:\n%s", want, view)
				}
			}
			if strings.Contains(view, `task "aux-0"`) || strings.Count(view, "auxiliary compaction") != 1 || strings.Count(view, "inference provider ") != 3 {
				t.Fatalf("evicted evidence visible or duplicate sections:\n%s", view)
			}
			if !reflect.DeepEqual(ui.UsageSnapshot(), beforeUsage) || !reflect.DeepEqual(ui.ProviderObservations(), beforeMain) {
				t.Fatal("render changed main diagnostics or spend")
			}
			if tc.width > 56 {
				auxX, signalsX := -1, -1
				for line := range strings.SplitSeq(view, "\n") {
					if x := strings.Index(line, "auxiliary compaction"); x >= 0 {
						auxX = x
					}
					if x := strings.Index(line, "signals"); x >= 0 {
						signalsX = x
					}
				}
				if auxX < 0 || signalsX < 0 || (tc.width == 84 && auxX >= signalsX) || (tc.width == 168 && auxX != signalsX) {
					t.Fatalf("unexpected layout: auxiliary x=%d, signals x=%d", auxX, signalsX)
				}
			}
		})
	}
}

func TestCompactionObservationsRenderEscapesAndFits(t *testing.T) {
	for _, hostile := range []string{"x\ny", "x\ry", "x\ty", "x\x1b[2Jy", "x\u009by", "x\u202ey", "x\"\\y"} {
		t.Run(fmt.Sprintf("%q", hostile), func(t *testing.T) {
			ui := tui.New()
			step(t, ui, teasink.CompactionSettledMsg{CompactionSettled: runner.CompactionSettled{TaskID: taskscope.ID(hostile), Inference: compact.Inference{
				Provider: hostile, Model: hostile,
				Execution: &llm.ExecutionObservation{GatewayDeployment: llm.DeploymentID(hostile), ReportedModel: llm.ModelID(hostile),
					GatewayVersion: llm.GatewayVersionID(hostile), ReportedVersion: llm.ModelVersionID(hostile), ReportedCost: llm.ReportedCost{Reported: true, Currency: llm.Currency(hostile)}},
			}}})
			view := compactionDashboard(t, ui, 56)
			for _, label := range []string{"task ", "inference provider ", "inference model ", "observed deployment ", "reported model ", "gateway runtime version ", "reported model version ", "currency "} {
				if !strings.Contains(view, label+fmt.Sprintf("%q", hostile)) {
					t.Errorf("missing quoted %q in:\n%s", label, view)
				}
			}
			for _, width := range []int{36, 48, 56, 84, 168} {
				step(t, ui, tea.WindowSizeMsg{Width: width, Height: 240})
				assertCompactionViewFits(t, ui.View().Content, width)
			}
		})
	}
	ui := tui.New()
	longID := strings.Repeat("long-aux-model-", 30)
	step(t, ui, teasink.CompactionSettledMsg{CompactionSettled: runner.CompactionSettled{Inference: compact.Inference{Provider: longID, Model: longID}}})
	_ = compactionDashboard(t, ui, 56)
	for _, width := range []int{36, 48, 56, 84, 168} {
		step(t, ui, tea.WindowSizeMsg{Width: width, Height: 240})
		raw := ui.View().Content
		assertCompactionViewFits(t, raw, width)
		if view := ansi.Strip(raw); strings.Contains(view, longID) || !strings.Contains(view, "…") || !strings.Contains(view, `inference model "long-aux-`) {
			t.Fatalf("long inference labels not truncated at width %d:\n%s", width, view)
		}
	}
}

func assertCompactionViewFits(t *testing.T, raw string, width int) {
	t.Helper()
	if strings.Contains(raw, "\x1b[2J") || strings.ContainsAny(ansi.Strip(raw), "\r\t\u009b\u202e") {
		t.Fatalf("terminal control injection at width %d", width)
	}
	for line := range strings.SplitSeq(raw, "\n") {
		if got := ansi.StringWidth(line); got > width {
			t.Fatalf("view line exceeds %d columns: %d: %q", width, got, line)
		}
	}
}
