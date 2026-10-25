package tui_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestPerformanceDiagnosticsOwnAndDeduplicateObservations(t *testing.T) {
	ui := tui.New()
	step(t, ui, teasink.ConversationStartedMsg{TaskID: "root", Provider: "openai", Model: "model"})
	step(t, ui, teasink.ConversationStartedMsg{TaskID: "child", Depth: 1})
	zero := time.Duration(0)
	usage := &llm.Usage{PromptTokens: 100, CompletionTokens: 20, CachedTokens: 60}
	attempt := teasink.ProviderAttemptSettledMsg{TaskID: "root", Attempt: 1, Usage: usage, Duration: 3 * time.Second, CallbackDuration: time.Second, TimeToFirstOutput: &zero}
	step(t, ui, attempt)
	step(t, ui, attempt)
	zero, usage.CachedTokens = time.Hour, 999
	p := ui.PerformanceDiagnostics()
	if p.Attempts != 1 || p.ProviderDuration != 3*time.Second || p.CallbackDuration != time.Second || p.TimeToFirstOutput == nil || *p.TimeToFirstOutput != 0 || p.LastAttemptUsage.CachedTokens != 60 {
		t.Fatalf("attempt=%+v", p)
	}
	*p.TimeToFirstOutput = time.Hour
	if *ui.PerformanceDiagnostics().TimeToFirstOutput != 0 {
		t.Fatal("snapshot aliases owner")
	}
	for _, event := range []teasink.ProviderAttemptSettledMsg{
		{TaskID: "unknown", Attempt: 100, Duration: time.Hour},
		{TaskID: "root", Depth: 1, Attempt: 100, Duration: time.Hour},
		{TaskID: "child", Depth: 1, Attempt: 1, Duration: time.Hour},
	} {
		step(t, ui, event)
	}
	step(t, ui, teasink.ProviderAttemptSettledMsg{TaskID: "root", Attempt: 2, Duration: 2 * time.Second, Reason: runner.TerminalError})
	p = ui.PerformanceDiagnostics()
	if p.Attempts != 2 || p.ProviderDuration != 5*time.Second || p.AttemptsWithUsage != 1 || p.TimeToFirstOutput != nil || p.LastAttemptUsageReported || p.LastAttemptUsage.CachedTokens != 0 {
		t.Fatalf("missing usage/retry=%+v", p)
	}
	iteration := teasink.IterationCompletedMsg{TaskID: "root", Iter: 0, RequestPreparationDuration: time.Second, ToolDispatchDuration: 2 * time.Second, ToolSurface: runner.ToolSurface{Count: 5, JSONBytes: 1000, Fingerprint: "stable"}}
	step(t, ui, iteration)
	step(t, ui, iteration)
	for _, event := range []teasink.IterationCompletedMsg{
		{TaskID: "unknown", Iter: 100, RequestPreparationDuration: time.Hour, Delta: &llm.Usage{CompletionTokens: 999}},
		{TaskID: "root", Depth: 1, Iter: 100, RequestPreparationDuration: time.Hour},
		{TaskID: "child", Depth: 1, Iter: 0, RequestPreparationDuration: time.Hour},
	} {
		step(t, ui, event)
	}
	step(t, ui, teasink.IterationCompletedMsg{TaskID: "root", Iter: 1, RequestPreparationDuration: 2 * time.Second, ToolDispatchDuration: time.Second, ToolSurface: runner.ToolSurface{Count: 6, Fingerprint: "changed", Changed: true}})
	step(t, ui, iteration) // out-of-order old iteration must not replace the surface
	if ui.PerformanceDiagnostics().ReportedCompletionTokens != 20 {
		t.Fatal("unaccepted iteration contaminated attempt token totals")
	}
	step(t, ui, teasink.CompactionAppliedMsg{TaskID: "root", BytesTrimmed: 100})
	step(t, ui, teasink.CompactionAppliedMsg{TaskID: "child", Depth: 1})
	ui.SetPlanMode(true)
	step(t, ui, teasink.ConversationEndedMsg{TaskID: "root", Duration: 10 * time.Second, TotalUsage: &llm.Usage{CompletionTokens: 50}, Reason: runner.TerminalCompleted})
	p = ui.PerformanceDiagnostics()
	if p.CompletedIterations != 2 || p.RequestPreparationDuration != 3*time.Second || p.ToolDispatchDuration != 3*time.Second || p.ToolSurface.Fingerprint != "changed" || p.Compactions != 1 || p.CurrentMode != "PLAN" || !p.Settled || p.ReportedTokensPerSecond != 5 {
		t.Fatalf("settled=%+v", p)
	}
	step(t, ui, attempt)
	step(t, ui, iteration)
	if !reflect.DeepEqual(p, ui.PerformanceDiagnostics()) {
		t.Fatal("late events changed settled diagnostics")
	}
	step(t, ui, teasink.ConversationStartedMsg{TaskID: "next"})
	p = ui.PerformanceDiagnostics()
	if p.TaskID != "next" || p.Attempts != 0 || p.Compactions != 0 || p.CompletedIterations != 0 || p.ToolSurface.Fingerprint != "" || p.TimeToFirstOutput != nil {
		t.Fatalf("new turn did not reset=%+v", p)
	}
	step(t, ui, teasink.ConversationEndedMsg{TaskID: "next", Duration: time.Second})
	if ui.PerformanceDiagnostics().UsageReported {
		t.Fatal("missing terminal usage inherited previous tokens")
	}
}

func TestPerformanceDiagnosticsRenderTimingCaveats(t *testing.T) {
	ui := tui.New()
	step(t, ui, teasink.ConversationStartedMsg{TaskID: "root"})
	first := 200 * time.Millisecond
	step(t, ui, teasink.ProviderAttemptSettledMsg{TaskID: "root", Attempt: 1, Duration: 2 * time.Second, CallbackDuration: 100 * time.Millisecond, TimeToFirstOutput: &first, Usage: &llm.Usage{CachedTokens: 100}})
	step(t, ui, teasink.IterationCompletedMsg{TaskID: "root", Iter: 0})
	view := ansi.Strip(strings.Join(ui.RenderCockpitLines(100), "\n"))
	for _, want := range []string{"performance", "root only", "provider 2s", "callbacks 100ms (inside provider)", "first output 200ms", "partial phases", "attempt cache read 100", "observations, not causes", "not server decode speed"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q in %s", want, view)
		}
	}
	for _, width := range []int{20, 52, 80} {
		for _, line := range ui.RenderCockpitLines(width) {
			// Existing cockpit sections need at least 52 columns; timing lines
			// must not regress the supported cockpit widths.
			if width >= 52 && ansi.StringWidth(line) > width {
				t.Errorf("width %d overflow: %s", width, line)
			}
		}
	}
}
