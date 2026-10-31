package tui_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func sidebarFixture(width, height int) *tui.UI {
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: width, Height: height})
	spec := engine.ProviderSpec{Name: "openai", Model: "synthetic-model"}
	ui.SetProviderContext(spec, spec)
	ui.SetContextWindow(200000)
	ui.SetPressureConfig(200000, 40000)
	ui.Update(teasink.ConversationStartedMsg{TaskID: "summary", Prompt: "Inspect the panel"})
	ui.Update(teasink.IterationCompletedMsg{TaskID: "summary", Iter: 1, Usage: &llm.Usage{PromptTokens: 168000}})
	ui.SetPlan(code.Plan{Steps: []code.PlanStep{
		{Text: "Finished investigation", Status: code.StepStatuses.COMPLETED},
		{Text: "Current refinement", Status: code.StepStatuses.INPROGRESS},
		{Text: "Verify rendering", Status: code.StepStatuses.PENDING},
	}})
	return ui
}

func sidebarText(ui *tui.UI, width, height int) string {
	sideWidth := 32
	if width >= 160 {
		sideWidth = 56
	}
	return screenRegion(surfaceCanvas(ui, width, height), width-sideWidth, 0, width, height-3)
}

func screenRegion(canvas uv.ScreenBuffer, x0, y0, x1, y1 int) string {
	var out strings.Builder
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			out.WriteString(canvas.CellAt(x, y).Content)
		}
		out.WriteByte('\n')
	}
	return out.String()
}

func TestSidebarHeightPriorities(t *testing.T) {
	for _, height := range []int{14, 20, 28, 40} {
		t.Run(strconv.Itoa(height), func(t *testing.T) {
			ui := sidebarFixture(120, height)
			got := collectSidebar(ui, 120, height)
			for _, want := range []string{"session", "context", "compaction due", "1/3 complete", "Finished investigation", "Current refinement", "Verify rendering", "ctrl+l more", "rate unknown"} {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q:\n%s", want, got)
				}
			}
			for _, absent := range []string{"working", "iteration", "├", "┤"} {
				if strings.Contains(got, absent) {
					t.Errorf("unexpected %q:\n%s", absent, got)
				}
			}
			if !strings.Contains(ansi.Strip(ui.View().Content), "working") {
				t.Error("composer activity disappeared")
			}
		})
	}
}

func collectSidebar(ui *tui.UI, width, height int) string {
	ui.Update(tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModAlt})
	var collected strings.Builder
	previous := ""
	for range 300 {
		view := sidebarText(ui, width, height)
		if view == previous {
			break
		}
		collected.WriteString(view)
		previous = view
		ui.Update(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModAlt})
	}
	return collected.String()
}

func TestSidebarGeometryThemeAndToggle(t *testing.T) {
	defer tui.UseTheme(theme.Theme{})
	for _, name := range []string{"midnight", "parchment"} {
		th, _ := theme.ByName(name)
		tui.UseTheme(th)
		for _, width := range []int{103, 104, 159, 160, 220, 280} {
			t.Run(fmt.Sprintf("%s/%d", name, width), func(t *testing.T) {
				ui := sidebarFixture(width, 40)
				canvas := surfaceCanvas(ui, width, 40)
				if width >= 104 {
					sw := 32
					if width >= 160 {
						sw = 56
					}
					x := width - sw
					for y := range 36 {
						if canvas.CellAt(x, y).Content != "│" {
							t.Fatalf("separator missing at %d,%d", x, y)
						}
					}
					heading := canvas.CellAt(x+2, 0)
					if heading.Content != "s" || heading.Style.Attrs&uv.AttrBold != 0 {
						t.Fatalf("plain heading = %+v", heading)
					}
					if !sameSurface(heading.Style.Bg, canvas.CellAt(width-1, 0).Style.Bg) {
						t.Error("heading uses a separate surface")
					}
				} else if strings.Contains(canvas.String(), "ctrl+l more") {
					t.Error("sidebar did not collapse")
				}
				ui.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
				if strings.Contains(ansi.Strip(ui.View().Content), "ctrl+l more") {
					t.Error("Ctrl+B did not hide")
				}
				ui.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
				got := ansi.Strip(ui.View().Content)
				want := "ctrl+l more"
				if width < 104 {
					want = "enabled · widen terminal"
				}
				if !strings.Contains(got, want) {
					t.Errorf("toggle feedback missing %q:\n%s", want, got)
				}
			})
		}
	}
}

func TestSidebarFullPlan(t *testing.T) {
	ui := sidebarFixture(160, 40)
	steps := []code.PlanStep{
		{Text: "Earlier pending", Status: code.StepStatuses.PENDING},
		{Text: "Current 日本語 refinement with a long description " + strings.Repeat("detail ", 40) + "FINAL_DETAIL", Status: code.StepStatuses.INPROGRESS},
		{Text: "Next in sequence", Status: code.StepStatuses.PENDING},
	}
	for i := range 40 {
		steps = append(steps, code.PlanStep{Text: fmt.Sprintf("Completed historical work %02d", i), Status: code.StepStatuses.COMPLETED})
	}
	ui.SetPlan(code.Plan{Steps: steps, Explanation: "retained explanation"})
	initial := sidebarText(ui, 160, 40)
	if strings.Index(initial, "Earlier pending") >= strings.Index(initial, "Current 日本語") {
		t.Fatalf("plan order changed:\n%s", initial)
	}
	got := collectSidebar(ui, 160, 40)
	for _, want := range []string{"40/43 complete", "☐ Earlier pending", "▶ Current 日本語", "FINAL_DETAIL", "☐ Next in sequence", "✓ Completed historical work 00", "✓ Completed historical work 39", "latest update: retained explanation", "usage"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	ui.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if !strings.Contains(ansi.Strip(ui.View().Content), "Earlier pending") {
		t.Error("ctrl+p did not retain full plan")
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	for i := range steps {
		steps[i].Status = code.StepStatuses.COMPLETED
	}
	ui.SetPlan(code.Plan{Steps: steps})
	got = collectSidebar(ui, 160, 40)
	if !strings.Contains(got, "43/43 complete") || !strings.Contains(got, "✓ Earlier pending") || !strings.Contains(got, "✓ Completed historical work 39") {
		t.Errorf("completed steps lost:\n%s", got)
	}
	ui.SetPlan(code.Plan{})
	if got := sidebarText(ui, 160, 40); strings.Contains(got, "complete") {
		t.Errorf("empty plan invented progress:\n%s", got)
	}
}

func TestSidebarBillingLabels(t *testing.T) {
	for _, tc := range []struct {
		name, provider, want string
		input, output        float64
	}{
		{name: "unknown", provider: "unknown", want: "metered · rate unknown"},
		{name: "subscription", provider: "openai-codex", want: "subscription · unmetered"},
		{name: "local", provider: "llamacpp", want: "local · unmetered"},
		{name: "metered", provider: "openai", input: 1, output: 2, want: "base-rate estimate"},
		{name: "incomplete", provider: "openai", input: 1, want: "cost incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ui := tui.New()
			ui.Update(tea.WindowSizeMsg{Width: 120, Height: 28})
			ui.SetProvider(tc.provider)
			if tc.provider == "openai-codex" || tc.provider == "llamacpp" {
				ui.SetSettings(&engine.Settings{Registry: backends.NewRegistry()})
				spec := engine.ProviderSpec{Name: tc.provider, Model: "synthetic-model"}
				ui.SetProviderContext(spec, spec)
				ui.RefreshProviderPricing()
			} else {
				ui.SetPricing(tc.input, tc.output)
			}
			ui.Update(teasink.ConversationStartedMsg{TaskID: "billing", Provider: tc.provider})
			ui.Update(teasink.ConversationEndedMsg{TaskID: "billing", Reason: runner.TerminalCompleted, TotalUsage: &llm.Usage{PromptTokens: 1000, CompletionTokens: 100}})
			got := sidebarText(ui, 120, 28)
			if !strings.Contains(got, tc.want) {
				t.Errorf("missing %q:\n%s", tc.want, got)
			}
		})
	}
}

func TestDashboardRetainsMetadataAndScrollGeometry(t *testing.T) {
	for _, width := range []int{52, 70, 120} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			ui := sidebarFixture(width, 20)
			ui.Update(teasink.ConversationEndedMsg{TaskID: "summary", Reason: runner.TerminalCompleted, TotalUsage: &llm.Usage{PromptTokens: 168000, CompletionTokens: 1200, CacheCreationTokens: 100}})
			ui.SetWorkspace("/synthetic/workspace", "synthetic-model")
			ui.SetSessionIdentity("fixture", "Sidebar acceptance", false, time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
			ui.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
			var collected strings.Builder
			for range 180 {
				collected.WriteString(screenRegion(surfaceCanvas(ui, width, 20), 2, 2, width-2, 16))
				ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			}
			text := strings.Join(strings.Fields(collected.String()), "")
			for _, want := range []string{"provider", "openai", "model", "synthetic-model", "workspace", "/synthetic/workspace", "window", "compact at", "billing", "rate unknown", "started"} {
				if !strings.Contains(text, strings.ReplaceAll(want, " ", "")) {
					t.Errorf("missing retained %q", want)
				}
			}
			bottom := screenRegion(surfaceCanvas(ui, width, 20), 0, 0, width, 17)
			ui.Update(tea.KeyPressMsg{Code: tea.KeyHome})
			ui.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
			if got := screenRegion(surfaceCanvas(ui, width, 20), 0, 0, width, 17); got != bottom {
				t.Errorf("End differs from repeated Down:\n%s\nwant:\n%s", got, bottom)
			}
			ui.Update(tea.WindowSizeMsg{Width: width, Height: 30})
			ui.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
			ui.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
			if got := ansi.Strip(ui.View().Content); !strings.Contains(got, "started") && width > 55 {
				t.Errorf("resize lost bottom metadata:\n%s", got)
			}
			for range 4 {
				ui.Update(tea.KeyPressMsg{Code: tea.KeyRight})
			}
			if got := strings.SplitN(ansi.Strip(ui.View().Content), "\n", 2)[0]; !strings.Contains(got, "events") {
				t.Errorf("active narrow tab hidden:\n%s", got)
			}
		})
	}
}

func TestSidebarShortCostEstimateKeepsQualifier(t *testing.T) {
	for _, height := range []int{9, 10, 11, 12, 13, 14, 18, 28} {
		t.Run(strconv.Itoa(height), func(t *testing.T) {
			ui := sidebarFixture(120, height)
			ui.Update(teasink.ConversationEndedMsg{TaskID: "summary", Reason: runner.TerminalCompleted})
			ui.SetPricing(1, 0)
			ui.Update(teasink.ConversationStartedMsg{TaskID: "partial"})
			ui.Update(teasink.IterationCompletedMsg{TaskID: "partial", Usage: &llm.Usage{PromptTokens: 168000}})
			ui.Update(teasink.ConversationEndedMsg{TaskID: "partial", Reason: runner.TerminalCompleted, TotalUsage: &llm.Usage{PromptTokens: 1000, CompletionTokens: 100}})
			got := collectSidebar(ui, 120, height)
			if strings.Contains(got, "$") && !strings.Contains(got, "cost incomplete") {
				t.Errorf("amount shown without qualifier:\n%s", got)
			}
			if !strings.Contains(got, "compaction due") {
				t.Errorf("pressure warning lost:\n%s", got)
			}
		})
	}
}

// Inspect painted cells, including the last text column beside the scrollbar.
func TestDashboardNarrowMetadataPreservesRightEdge(t *testing.T) {
	const width, height = 52, 28
	const model = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz-END"
	ui := sidebarFixture(width, height)
	ui.SetProviderContext(engine.ProviderSpec{Name: "openai", Model: model}, engine.ProviderSpec{Name: "openai", Model: model})
	ui.SetPricing(1, 2)
	ui.Update(teasink.ConversationEndedMsg{TaskID: "summary", Reason: runner.TerminalCompleted, TotalUsage: &llm.Usage{PromptTokens: 168000, CompletionTokens: 1200}})
	ui.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	var collected strings.Builder
	for range 120 {
		collected.WriteString(screenRegion(surfaceCanvas(ui, width, height), 2, 2, width-2, height-4))
		ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	text := strings.Join(strings.Fields(collected.String()), "")
	for _, want := range []string{model, "base-rateestimate"} {
		if !strings.Contains(text, want) {
			t.Errorf("painted metadata lost characters from %q:\n%s", want, collected.String())
		}
	}
}
