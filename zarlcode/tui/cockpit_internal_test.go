package tui

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestFoldTurnComplete(t *testing.T) {
	s := RunState{window: 1000, inCostPer1k: 0.01, outCostPer1k: 0.03}
	s.Running = true
	// Per-iteration values (from IterationCompleted) snapshot lastIn / lastOut /
	// liveCtx. foldTurnComplete receives the cumulative totalUsage (same values
	// for a single-iteration run) and only accumulates session totals.
	// A usage-bearing iteration: occupancy and delta carry the same value.
	iterUsage := &llm.Usage{
		PromptTokens:     600,
		CompletionTokens: 100,
		TotalTokens:      700,
		CachedTokens:     200,
	}
	s.foldIteration(iterUsage, iterUsage)
	s.foldTurnComplete(&llm.Usage{
		PromptTokens:     600,
		CompletionTokens: 100,
		TotalTokens:      700,
		CachedTokens:     200,
	}, 2*time.Second, 3)

	if s.Running {
		t.Error("running should clear on turn complete")
	}
	if s.lastIn != 600 || s.lastOut != 100 || s.lastCached != 200 {
		t.Errorf("last turn = in%d out%d cached%d", s.lastIn, s.lastOut, s.lastCached)
	}
	if s.lastIter != 3 {
		t.Errorf("lastIter = %d, want 3", s.lastIter)
	}
	if s.sessionTurns != 1 || s.sessionIn != 600 || s.sessionCached != 200 {
		t.Errorf("session = turns%d in%d cached%d", s.sessionTurns, s.sessionIn, s.sessionCached)
	}
	if !approx(s.fillFrac(), 0.6) {
		t.Errorf("fillFrac = %v, want 0.6", s.fillFrac())
	}
	if !approx(s.tokPerSec(), 50) {
		t.Errorf("tokPerSec = %v, want 50", s.tokPerSec())
	}
	if len(s.history) != 1 {
		t.Fatalf("history len = %d, want 1", len(s.history))
	}
	if !approx(s.history[0].fillFrac, 0.6) {
		t.Errorf("sample fillFrac = %v, want 0.6", s.history[0].fillFrac)
	}
}

func TestLiveTokPerSecUsesProviderCompletionTokens(t *testing.T) {
	s := RunState{Running: true, turnStartedAt: time.Now().Add(-2 * time.Second), turnOutBytes: 8}
	delta := &llm.Usage{CompletionTokens: 120}
	s.foldIteration(delta, delta)

	got := s.liveTokPerSec()
	if got < 55 || got > 65 {
		t.Fatalf("liveTokPerSec = %.2f, want provider-token rate around 60", got)
	}
}

func TestCostMath(t *testing.T) {
	s := RunState{inCostPer1k: 0.01, outCostPer1k: 0.03}
	// fresh 400 @0.01/1k = 0.004; cached 200 @0.01*0.10/1k = 0.0002;
	// out 100 @0.03/1k = 0.003 → 0.0072.
	if got := s.cost(600, 200, 100); !approx(got, 0.0072) {
		t.Errorf("cost = %v, want 0.0072", got)
	}
	// cacheSaved: 200 cached @0.01/1k * (1-0.10) = 0.0018.
	s.sessionCached = 200
	if got := s.cacheSaved(); !approx(got, 0.0018) {
		t.Errorf("cacheSaved = %v, want 0.0018", got)
	}
}

func TestFoldCompactionSawtooth(t *testing.T) {
	s := RunState{window: 1000}
	s.liveCtx = 800
	// 400 bytes trimmed → 100 token-equivalent drop on the ctx gauge.
	s.foldCompaction(40, 28, 400, "tiered")

	if s.liveCtx != 700 {
		t.Errorf("liveCtx = %d, want 700 (dropped by bytes/4 token-equivalent)", s.liveCtx)
	}
	if s.compactBytes != 400 {
		t.Errorf("compactBytes = %d, want 400 (raw bytes for display)", s.compactBytes)
	}
	if !s.pendingCompact {
		t.Error("pendingCompact should arm after a compaction")
	}
	if s.compactBefore != 40 || s.compactAfter != 28 || s.compactEngine != "tiered" {
		t.Errorf("compaction record = %d→%d %q", s.compactBefore, s.compactAfter, s.compactEngine)
	}

	// The next recorded turn should carry the sawtooth flag.
	s.foldTurnComplete(&llm.Usage{PromptTokens: 700, TotalTokens: 700}, time.Second, 1)
	if s.pendingCompact {
		t.Error("pendingCompact should clear once consumed by a sample")
	}
	if !s.history[len(s.history)-1].compacted {
		t.Error("the post-compaction sample should be flagged compacted")
	}
}

func TestFoldTool(t *testing.T) {
	s := RunState{}
	s.foldTool("bash", time.Second, false)
	s.foldTool("bash", 2*time.Second, false)
	s.foldTool("bash", time.Second, true)
	s.foldTool("read_file", time.Second, false)

	if s.sessionToolCalls != 4 {
		t.Errorf("sessionToolCalls = %d, want 4", s.sessionToolCalls)
	}
	bash := s.toolStats["bash"]
	if bash.calls != 3 || bash.fails != 1 || bash.dur != 4*time.Second {
		t.Errorf("bash stat = %+v", bash)
	}
	top := s.topTools()
	if len(top) == 0 || top[0].name != "bash" {
		t.Errorf("top tool = %+v, want bash first", top)
	}
}

func TestFoldSubAgentUsageSessionOnly(t *testing.T) {
	s := RunState{window: 1000}
	s.foldSubAgentUsage(&llm.Usage{PromptTokens: 300, CompletionTokens: 50, CachedTokens: 10})
	if s.sessionIn != 300 || s.sessionOut != 50 || s.sessionCached != 10 {
		t.Errorf("session = in%d out%d cached%d", s.sessionIn, s.sessionOut, s.sessionCached)
	}
	// Sub-agent usage must NOT disturb the top-level gauge or last-turn.
	if s.liveCtx != 0 || s.lastIn != 0 || s.sessionTurns != 0 {
		t.Errorf("sub-agent usage leaked into top-level: liveCtx%d lastIn%d turns%d",
			s.liveCtx, s.lastIn, s.sessionTurns)
	}
}

func TestResetPreservesSession(t *testing.T) {
	s := RunState{window: 1000, inCostPer1k: 0.01}
	s.foldTurnComplete(&llm.Usage{PromptTokens: 500, TotalTokens: 500}, time.Second, 1)
	s.iterations = 5
	s.Running = true

	s.reset()

	if s.iterations != 0 || s.Running {
		t.Error("reset should clear live-run counters")
	}
	if s.sessionTurns != 1 || len(s.history) != 1 || s.window != 1000 || s.inCostPer1k != 0.01 {
		t.Error("reset must preserve session totals, history, window, and pricing")
	}
}

func TestCockpitLinesEmptyState(t *testing.T) {
	m := New()
	got := strings.Join(m.cockpitLines(46), "\n")
	// No usage yet → no placeholder rows or empty gauges; the context /
	// last-turn / usage sections are simply absent.
	for _, ph := range []string{"no run yet", "no usage", "no throughput", "no turn"} {
		if strings.Contains(got, ph) {
			t.Errorf("empty cockpit should omit placeholders, found %q in:\n%s", ph, got)
		}
	}
	if strings.Contains(got, "context") {
		t.Errorf("empty cockpit should not render the context section, got:\n%s", got)
	}
}

func TestCockpitLinesLLMStateOverview(t *testing.T) {
	old := palette
	UseTheme(theme.Theme{Secondary: "#123456"})
	t.Cleanup(func() { UseTheme(old) })

	m := New()
	m.session.Provider = "openai"
	m.session.Model = "gpt-4o-mini"
	m.session.Workspace = "~/src/project"
	m.session.Branch = "main"

	got := strings.Join(m.cockpitLines(46), "\n")
	// Session state is a real overview, not the old abbreviated pvd/mdl/ws rows.
	for _, want := range []string{"provider", "model", "window", "billing", "workspace", "openai", "gpt-4o-mini", "~/src/project", "main"} {
		if !strings.Contains(got, want) {
			t.Errorf("cockpit missing LLM state %q:\n%s", want, got)
		}
	}
	for _, old := range []string{"[llm state]", "mode      ", "pvd", "mdl"} {
		if strings.Contains(got, old) {
			t.Errorf("cockpit should not render removed sidebar label %q:\n%s", old, got)
		}
	}
	if !strings.Contains(got, theme.Color("#123456").FG()+"main") {
		t.Errorf("cockpit should render git branch in secondary colour:\n%q", got)
	}
}

func TestCockpitLinesSections(t *testing.T) {
	m := New()
	m.SetPricing(0.01, 0.03)
	// Two completed turns so sparklines and history exist.
	// Per-iteration snapshot from foldIteration; foldTurnComplete accumulates session.
	m.session.Run.liveCtx = 600
	m.session.Run.foldTool("bash", time.Second, false)
	turn1 := &llm.Usage{PromptTokens: 500, CompletionTokens: 80, TotalTokens: 580, CachedTokens: 200}
	m.session.Run.foldIteration(turn1, turn1)
	m.session.Run.foldTurnComplete(turn1, time.Second, 2)
	turn2 := &llm.Usage{PromptTokens: 600, CompletionTokens: 90, TotalTokens: 690, CachedTokens: 250}
	m.session.Run.foldIteration(turn2, turn2)
	m.session.Run.foldTurnComplete(&llm.Usage{PromptTokens: 600, CompletionTokens: 90, TotalTokens: 690, CachedTokens: 250}, time.Second, 2)

	got := strings.Join(m.cockpitLines(56), "\n")
	for _, want := range []string{"[context]", "[last turn]", "[cost]", "[tools]", "cached", "tok/s", "cache"} {
		if !strings.Contains(got, want) {
			t.Errorf("cockpit missing %q section/label, got:\n%s", want, got)
		}
	}
}

func TestContextRoleGraph(t *testing.T) {
	m := New()
	m.session.Run.window = 1000
	m.session.Run.liveCtx = 600
	m.session.Run.setContextBreakdown(&runner.ContextBreakdown{
		SystemBytes: 400, UserBytes: 200, AssistantBytes: 800, ToolBytes: 1200,
		SkillBytes: 400, AgentBytes: 400,
		SystemMsgs: 1, UserMsgs: 2, AssistantMsgs: 3, ToolMsgs: 4,
	})

	if !m.session.Run.hasBreakdown() {
		t.Fatal("hasBreakdown should be true after setContextBreakdown")
	}
	if got := width(contextRoleBar(&m.session.Run, 40)); got != 40 {
		t.Errorf("role bar width = %d, want exactly 40", got)
	}

	out := strings.Join(m.cockpitLines(46), "\n")
	for _, w := range []string{"sys", "usr", "asst", "tool", "skills", "agents"} {
		if !strings.Contains(out, w) {
			t.Errorf("role graph missing %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "fresh") {
		t.Errorf("breakdown present — should not fall back to cached/fresh/free split:\n%s", out)
	}
}

func TestContextRoleGraphFallback(t *testing.T) {
	// No breakdown → the cached/fresh/free split is the fallback graph.
	m := New()
	m.session.Run.window = 1000
	m.session.Run.liveCtx = 400
	m.session.Run.lastCached = 150
	out := strings.Join(m.cockpitLines(46), "\n")
	if !strings.Contains(out, "fresh") || !strings.Contains(out, "cached") {
		t.Errorf("zero-breakdown cockpit should show cached/fresh/free fallback:\n%s", out)
	}
}

func TestDashboardColumns(t *testing.T) {
	m := New()
	m.session.Run.foldTurnComplete(&llm.Usage{PromptTokens: 500, TotalTokens: 500}, time.Second, 1)

	if cols := m.dashboardColumns(3, 30); len(cols) != 3 {
		t.Errorf("3-col dashboard returned %d columns", len(cols))
	}
	if cols := m.dashboardColumns(2, 40); len(cols) != 2 {
		t.Errorf("2-col dashboard returned %d columns", len(cols))
	}
	if cols := m.dashboardColumns(1, 60); len(cols) != 1 {
		t.Errorf("1-col dashboard returned %d columns", len(cols))
	}
}

func TestDashboardMaxScroll(t *testing.T) {
	cols := [][]string{
		{"a", "b", "c", "d"},
		{"a"},
	}
	if got := dashboardMaxScroll(cols, 2); got != 2 {
		t.Fatalf("dashboard max scroll = %d, want 2", got)
	}
	if got := dashboardMaxScroll(cols, 10); got != 0 {
		t.Fatalf("dashboard max scroll with tall viewport = %d, want 0", got)
	}
}

func TestDashboardScrollClampsToContent(t *testing.T) {
	m := New()
	m.width, m.height = 100, 14
	m.layout = computeLayout(m.width, m.height)
	for i := range 20 {
		m.session.Run.foldTurnComplete(&llm.Usage{PromptTokens: 500 + i, CompletionTokens: 50, TotalTokens: 550 + i}, time.Second, 1)
	}

	maxScroll := m.dashboardMaxScroll()
	if maxScroll == 0 {
		t.Fatal("test setup should produce an overflowing dashboard")
	}
	m.contextView.setActiveScroll(maxScroll + 10)
	m.clampDashboardScroll()
	if m.contextView.activeScroll() != maxScroll {
		t.Fatalf("context view scroll = %d, want clamped max %d", m.contextView.activeScroll(), maxScroll)
	}
	m.contextView.setActiveScroll(-10)
	m.clampDashboardScroll()
	if m.contextView.activeScroll() != 0 {
		t.Fatalf("context view scroll = %d, want clamped zero", m.contextView.activeScroll())
	}
}
