package tui_test

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestPerformanceDiagnosticsOwnedAndRootOnly(t *testing.T) {
	ui := tui.New()
	step(t, ui, teasink.ConversationStartedMsg{TaskID: "root"})
	step(t, ui, teasink.ConversationStartedMsg{TaskID: "child", Depth: 1})
	zero := time.Duration(0)
	usage := &llm.Usage{CompletionTokens: 20, CachedTokens: 60}
	attempt := teasink.ProviderAttemptSettledMsg{TaskID: "root", Attempt: 1, Usage: usage, Duration: 3 * time.Second, CallbackDuration: time.Second, TimeToFirstOutput: &zero}
	step(t, ui, attempt)
	step(t, ui, attempt)
	zero, usage.CachedTokens = time.Hour, 999
	p := ui.PerformanceDiagnostics()
	if p.Attempts != 1 || p.ProviderDuration != 3*time.Second || p.TimeToFirstOutput == nil || *p.TimeToFirstOutput != 0 || p.LastAttemptUsage.CachedTokens != 60 {
		t.Fatalf("attempt=%+v", p)
	}
	*p.TimeToFirstOutput = time.Hour
	if *ui.PerformanceDiagnostics().TimeToFirstOutput != 0 {
		t.Fatal("snapshot aliases owner")
	}
	step(t, ui, teasink.ProviderAttemptSettledMsg{TaskID: "child", Depth: 1, Attempt: 1, Duration: time.Hour})
	step(t, ui, teasink.ProviderAttemptSettledMsg{TaskID: "root", Attempt: 2, Duration: 2 * time.Second})
	iteration := teasink.IterationCompletedMsg{TaskID: "root", Iter: 0, RequestPreparationDuration: time.Second, ToolDispatchDuration: 2 * time.Second, ToolSurface: runner.ToolSurface{Count: 5}}
	step(t, ui, iteration)
	step(t, ui, iteration)
	step(t, ui, teasink.IterationCompletedMsg{TaskID: "child", Depth: 1, Iter: 0, RequestPreparationDuration: time.Hour})
	step(t, ui, teasink.CompactionAppliedMsg{TaskID: "root"})
	step(t, ui, teasink.ConversationEndedMsg{TaskID: "root", Duration: 10 * time.Second, TotalUsage: &llm.Usage{CompletionTokens: 50}})
	p = ui.PerformanceDiagnostics()
	if p.Attempts != 2 || p.ProviderDuration != 5*time.Second || p.CompletedIterations != 1 || p.RequestPreparationDuration != time.Second || p.Compactions != 1 || !p.Settled || p.ReportedTokensPerSecond != 5 {
		t.Fatalf("settled=%+v", p)
	}
	step(t, ui, attempt)
	if ui.PerformanceDiagnostics().Attempts != 2 {
		t.Fatal("late attempt changed settled diagnostics")
	}
	step(t, ui, teasink.ConversationStartedMsg{TaskID: "next"})
	if p := ui.PerformanceDiagnostics(); p.TaskID != "next" || p.Attempts != 0 || p.CompletedIterations != 0 {
		t.Fatalf("reset=%+v", p)
	}
}

func TestPerformanceDiagnosticsRenderCaveats(t *testing.T) {
	ui := tui.New()
	step(t, ui, teasink.ConversationStartedMsg{TaskID: "root"})
	view := ansi.Strip(strings.Join(ui.RenderCockpitLines(100), "\n"))
	for _, want := range []string{"performance", "root only", "partial phases", "observations, not causes", "not server decode speed"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q in %s", want, view)
		}
	}
}
