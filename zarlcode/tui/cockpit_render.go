package tui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

// cockpitLines renders the detailed single-column dashboard body. The compact
// session sidebar uses separate, height-aware summaries.
func (m *UI) cockpitLines(width int) []string {
	s := &m.session.Run

	var out []string
	add := func(ss ...string) { out = append(out, ss...) }

	// --- LLM/session overview ---------------------------------------------
	add(sidebarHeading("session"))
	add(m.llmStateLines()...)

	add("")

	// --- context -------------------------------------------------------
	// Keep the context composition graph as the primary visual: it is the fastest
	// way to understand what is occupying the window. The surrounding text carries
	// pressure / compaction hints so the graph itself can stay clean.
	add(sidebarHeading("context"))
	add(s.contextHeadline())
	if s.hasBreakdown() {
		add(contextRoleBar(s, width))
		add("") // breathing room between the graph and its key
		add(contextRoleLegend(s)...)
	} else {
		add(contextSplitBar(s, width))
		add("")
		add(contextSplitLegend(s))
	}
	if pressure := s.compactionPressureLine(); pressure != "" {
		add(pressure)
	}

	// --- COMPACTION ----------------------------------------------------
	if s.compactions > 0 {
		add("", sidebarHeading("compaction"))
		add(s.compactionLines()...)
	}

	// --- LAST TURN -----------------------------------------------------
	if s.lastTotal > 0 || s.lastIn > 0 {
		add("", sidebarHeading("last turn"))
		add(s.lastTurnSummary())
		add(s.throughputLine(width))
	}

	// --- COST ----------------------------------------------------------
	add("", sidebarHeading("cost"))
	switch {
	case s.hasPricing() || s.hasSessionCost():
		add(s.costSummary())
	case s.subscription:
		add(palette.Muted.On("subscription — no metered cost"))
	case s.local:
		add(palette.Muted.On("local — no metered cost"))
	default:
		add(palette.Muted.On("metered · rate unknown"))
	}
	if s.hasLegacyCost() {
		add(s.legacyCostLine())
	}
	if s.legacyCacheSavedUSD > 0 {
		add(s.legacyCacheSavingsLine())
	}
	if s.hasCacheReporting() {
		add(s.cacheUsageLines(width)...)
	}

	// --- TOOLS ---------------------------------------------------------
	if tools := s.topTools(); len(tools) > 0 {
		add("", sidebarHeading("tools"))
		add(toolHistogram(tools, width, 12)...)
	}

	return out
}

func (m *UI) contextPaneLines(width, _ int) []string {
	s := &m.session.Run
	out := []string{sidebarHeading("context"), s.contextHeadline()}
	if s.hasBreakdown() {
		out = append(out, contextRoleBar(s, width), "")
		out = append(out, contextRoleLegend(s)...)
		out = append(out, "", sidebarHeading("cache"), contextSplitBar(s, width), "", contextSplitLegend(s))
	} else {
		out = append(out, contextSplitBar(s, width), "", contextSplitLegend(s))
	}
	if pressure := s.compactionPressureLine(); pressure != "" {
		out = append(out, pressure)
	}
	if s.compactions > 0 {
		out = append(out, "", sidebarHeading("compaction"))
		out = append(out, s.compactionLines()...)
	}
	return out
}

func (m *UI) contextPromptLines(width int) []string {
	snap := BuildInspectorSnapshot(m.appContext(), m.session, m.live, nil)
	out := []string{sidebarHeading("prompt")}
	stackLines := promptStackSummaryLines(snap.PromptStack)
	if len(stackLines) == 0 {
		out = append(out, palette.Muted.On("prompt stack unavailable"))
	} else {
		out = append(out, stackLines...)
	}
	if len(snap.Errors) > 0 {
		out = append(out, "", sidebarHeading("warnings"))
		for _, err := range snap.Errors {
			out = append(out, "  "+palette.Muted.On(err))
		}
	}
	if snap.PromptSystem == "" {
		return append(out, "", sidebarHeading("preview"), palette.Muted.On("prompt preview not available"))
	}
	preview := promptPreviewLines(snap.PromptSystem, width)
	if len(preview) > 0 {
		out = append(out, "", sidebarHeading("preview"))
		out = append(out, preview...)
	}
	return out
}

func promptPreviewLines(prompt string, width int) []string {
	var out []string
	for ln := range strings.SplitSeq(prompt, "\n") {
		trimmed := strings.TrimSpace(ln)
		if trimmed == "" || trimmed == "---" {
			continue
		}
		prefix := "  "
		style := palette.Muted.On
		if strings.HasPrefix(trimmed, "[") {
			style = palette.Info.On
		} else if strings.HasSuffix(trimmed, ":") || strings.HasPrefix(trimmed, "workspace:") || strings.HasPrefix(trimmed, "model:") {
			style = palette.Subtle.On
		}
		out = append(out, renderPlain(width, prefix+trimmed, withStyle(style))...)
		if len(out) >= 24 {
			out = append(out, palette.Subtle.On("  … prompt preview truncated"))
			break
		}
	}
	return out
}

func (m *UI) contextToolsLines(width int) []string {
	snap := BuildInspectorSnapshot(m.appContext(), m.session, m.live, nil)
	available, hidden := groupContextTools(snap.Tools, snap.PlanMode)
	out := []string{sidebarHeading("tools")}
	out = append(out, palette.Muted.On(fmt.Sprintf("%d available now · %d hidden", len(available), len(hidden))))
	if len(available) > 0 {
		out = append(out, "", sidebarHeading("available"))
		out = append(out, renderToolSpecRows(available)...)
	}
	if len(hidden) > 0 {
		out = append(out, "", sidebarHeading("plan-hidden"))
		out = append(out, renderToolSpecRows(hidden)...)
	}
	if top := m.session.Run.topTools(); len(top) > 0 {
		out = append(out, "", sidebarHeading("recent usage"))
		out = append(out, toolHistogram(top, width, max(12, len(top)))...)
	}
	return out
}

func groupContextTools(specs []tools.ToolSpec, plan bool) ([]tools.ToolSpec, []tools.ToolSpec) {
	var available, hidden []tools.ToolSpec
	for _, spec := range specs {
		if plan && !engine.PlanAllows(spec.Name) {
			hidden = append(hidden, spec)
			continue
		}
		available = append(available, spec)
	}
	slices.SortFunc(available, func(a, b tools.ToolSpec) int { return cmp.Compare(a.Name, b.Name) })
	slices.SortFunc(hidden, func(a, b tools.ToolSpec) int { return cmp.Compare(a.Name, b.Name) })
	return available, hidden
}

func renderToolSpecRows(specs []tools.ToolSpec) []string {
	out := make([]string, 0, len(specs)*2)
	for _, spec := range specs {
		out = append(out, "  "+palette.Info.On(string(spec.Name)))
		out = append(out, "    "+palette.Muted.On(spec.Description))
	}
	return out
}

func (m *UI) contextEventsLines(_ int) []string {
	snap := BuildInspectorSnapshot(m.appContext(), m.session, m.live, nil)
	groups := groupContextEvents(snap.EventLog)
	out := []string{sidebarHeading("events")}
	if len(snap.EventLog) == 0 {
		return append(out, palette.Muted.On("no events recorded yet"))
	}
	for _, g := range groups {
		if len(g.events) == 0 {
			continue
		}
		out = append(out, "", sidebarHeading(g.label))
		for _, e := range g.events {
			out = append(out, formatContextEventRow(e))
		}
	}
	return out
}

type contextEventGroup struct {
	label  string
	events []EventRingEntry
}

func groupContextEvents(events []EventRingEntry) []contextEventGroup {
	groups := []contextEventGroup{{label: "context", events: nil}, {label: "run", events: nil}, {label: "system", events: nil}}
	for _, e := range events {
		kind := strings.ToLower(e.Kind + " " + e.Detail)
		switch {
		case strings.Contains(kind, "compact") || strings.Contains(kind, "context"):
			groups[0].events = append(groups[0].events, e)
		case strings.Contains(kind, "tool") || strings.Contains(kind, "run") || strings.Contains(kind, "turn") || strings.Contains(kind, "agent"):
			groups[1].events = append(groups[1].events, e)
		default:
			groups[2].events = append(groups[2].events, e)
		}
	}
	return groups
}

func formatContextEventRow(e EventRingEntry) string {
	return fmt.Sprintf("  %s  %s  %s", palette.Subtle.On(e.At.Format("15:04:05")), palette.Primary.On(e.Kind), palette.Muted.On(e.Detail))
}

// llmStateLines retains the complete session identity and accounting metadata
// in the expanded dashboard at every responsive width.
func (m *UI) llmStateLines() []string {
	s := &m.session.Run
	out := make([]string, 0, 9)
	add := func(label, value string) {
		out = append(out, palette.Subtle.On(padRight(label, 10))+value)
	}

	if name := strings.TrimSpace(m.session.Label); name != "" {
		add("name", palette.Fg.On(name))
	}

	provider := m.session.Provider
	if provider == "" {
		provider = "not configured"
	}
	add("provider", palette.Fg.On(provider))

	model := m.session.Model
	if model == "" {
		model = "not selected"
	}
	add("model", palette.Fg.On(model))

	if s.window > 0 {
		window := palette.Fg.On(fmtCount(s.window)) + palette.Muted.On(" token window")
		if threshold, _, ok := s.compactionThreshold(); ok {
			window += palette.Muted.On(" · compact at ") + palette.Subtle.On(fmtCount(threshold))
		}
		add("window", window)
	} else {
		add("window", palette.Muted.On("unknown"))
	}

	if effort := m.reasoningEffortLine(); effort != "" {
		add("reasoning", effort)
	}
	add("billing", s.billingLine())
	if m.session.Workspace != "" {
		add("workspace", palette.Fg.On(m.session.Workspace))
	}
	if m.session.Branch != "" {
		add("branch", palette.Secondary.On(m.session.Branch))
	}
	if pr := m.session.PR; pr != nil {
		add("pr", prLine(pr))
	}
	if changes := m.workspaceChangesLine(); changes != "" {
		add("changes", changes)
	}
	if totals := s.sessionTotalsLine(); totals != "" {
		add("session", totals)
	}
	if s.sessionToolCalls > 0 {
		add("tools", s.toolsSummaryLine())
	}
	if !m.session.StartedAt.IsZero() {
		started := palette.Fg.On(m.session.StartedAt.Format("15:04")) +
			palette.Muted.On(" · ") + palette.Subtle.On(fmtAgo(time.Since(m.session.StartedAt)))
		add("started", started)
	}

	return out
}

func (m *UI) reasoningEffortLine() string {
	if m.session.Provider != backends.NameOpenAICodex.String() {
		return ""
	}
	effort := strings.TrimSpace(m.session.ProvSpec.CodexEffort)
	for _, supported := range openaicodex.EffortVariants(m.session.Model) {
		if effort == supported {
			return palette.Fg.On(effort)
		}
	}
	for _, effort := range openaicodex.EffortVariants(m.session.Model) {
		return palette.Fg.On("auto") + palette.Muted.On(" · ") + palette.Subtle.On(effort)
	}
	return palette.Fg.On("auto") + palette.Muted.On(" · model default")
}

func (m *UI) workspaceChangesLine() string {
	if m.session == nil || m.session.WorkingSet == nil {
		return ""
	}
	// Both collections are owned by the UI event loop. Counts need neither the
	// sorted file snapshot nor a copy of the session's mutation history.
	files := len(m.session.WorkingSet.files)
	if files == 0 {
		return ""
	}
	mutations := len(m.session.WorkingSet.mutations)
	parts := []string{palette.Fg.On(fmtCount(files)) + palette.Subtle.On(" files")}
	if mutations > 0 {
		parts = append(parts, palette.Fg.On(fmtCount(mutations))+palette.Subtle.On(" edits"))
	}
	return strings.Join(parts, palette.Muted.On(" · "))
}

// cockpitStatusLine is the one-line "what's happening now" row: a braille
// activity indicator, the run state, and the live iteration / tool counters.
func (m *UI) cockpitStatusLine() string {
	s := &m.session.Run
	glyph, label := palette.Muted.On(runActivityGlyph(m.frame, false)), palette.Muted.On("idle")
	if s.Running {
		glyph, label = palette.Success.On(runActivityGlyph(m.frame, true)), palette.Success.On(s.activityLabel())
	}
	parts := []string{glyph + " " + label}
	if s.Running && !s.turnStartedAt.IsZero() {
		parts = append(parts, palette.Subtle.On("turn "+compactElapsed(time.Since(s.turnStartedAt))))
		if !s.iterationStartedAt.IsZero() {
			parts = append(parts, palette.Subtle.On("iteration "+compactElapsed(time.Since(s.iterationStartedAt))))
		}
	}
	if s.iterations > 0 {
		parts = append(parts, palette.Subtle.On(itoa(s.iterations)+" iter"))
	}
	if s.tools > 0 || s.toolsRunning > 0 {
		t := palette.Subtle.On(itoa(s.tools) + " tools")
		if s.toolsRunning > 0 {
			t += palette.Warning.On(" +" + itoa(s.toolsRunning))
		}
		parts = append(parts, t)
	}
	if s.maxDepth > 0 {
		parts = append(parts, palette.Secondary.On("d"+itoa(s.maxDepth)))
	}
	return strings.Join(parts, palette.Muted.On(" · "))
}

func compactElapsed(d time.Duration) string {
	if d < time.Second {
		return "0s"
	}
	return d.Round(time.Second).String()
}

// contextSummary is the headline pressure row: percent-full (pressure
// coloured) · used / window · free headroom.
func (s *RunState) contextSummary() string {
	used := s.effectiveUsed()
	pct := 0
	if s.window > 0 {
		pct = used * 100 / s.window
	}
	head := pressureColor(s.fillFrac()).On(itoa(pct) + "% full")
	disp := palette.Fg.On(fmtCount(used)) + palette.Muted.On(" / ") + palette.Fg.On(fmtCount(s.window))
	line := head + palette.Muted.On(" · ") + disp
	if s.window > 0 {
		free := max(s.window-used, 0)
		line += palette.Muted.On(" · ") + palette.Subtle.On(fmtCount(free)+" free")
	}
	return line
}

func (s *RunState) compactionThreshold() (int, int, bool) {
	var threshold, remaining int
	if s.pressureWindow <= 0 || s.pressureReserve < 0 {
		return 0, 0, false
	}
	threshold = s.pressureWindow - s.pressureReserve
	if threshold <= 0 || threshold >= s.pressureWindow {
		return 0, 0, false
	}
	remaining = max(threshold-s.effectiveUsed(), 0)
	return threshold, remaining, true
}

func (s *RunState) compactionPressureLine() string {
	threshold, remaining, ok := s.compactionThreshold()
	if !ok {
		return ""
	}
	if remaining == 0 {
		return palette.Warning.On("compaction due") + palette.Muted.On(" · threshold ") + palette.Subtle.On(fmtCount(threshold))
	}
	return palette.Subtle.On("compact in ") + palette.Fg.On(fmtCount(remaining)) +
		palette.Muted.On(" · threshold ") + palette.Subtle.On(fmtCount(threshold))
}

// contextRoleBar paints the v1-style composition graph: the context window
// partitioned by role (system / user / assistant) with tool content further
// split into skill_load (skills) / agent_spawn (agents) / instruction_load
// (instructions) / other tool output,
// then free headroom. Per-role token estimates (bytes/4) are scaled to sum
// to the provider-authoritative used count so the bar's free share matches
// the gauge above it; rounding drift lands in the free segment.
func contextRoleBar(s *RunState, width int) string {
	sys := float64(s.ctxSysBytes) / 4
	user := float64(s.ctxUserBytes) / 4
	asst := float64(s.ctxAsstBytes) / 4
	tool := float64(s.ctxToolBytes) / 4
	skill := float64(s.ctxSkillBytes) / 4
	agent := float64(s.ctxAgentBytes) / 4
	instruction := float64(s.ctxInstructionBytes) / 4
	other := tool - skill - agent - instruction
	if other < 0 {
		other = 0
	}

	roleSum := sys + user + asst + tool
	used := float64(s.effectiveUsed())
	scale := 1.0
	if used > 0 && roleSum > 0 {
		scale = used / roleSum
	}
	free := float64(s.window) - used
	if free < 0 {
		free = 0
	}
	return s.markThresholdBar(stackedBar([]barSeg{
		{weight: sys * scale, color: palette.System, glyph: '█'},
		{weight: user * scale, color: palette.User, glyph: '█'},
		{weight: asst * scale, color: palette.Assistant, glyph: '█'},
		{weight: skill * scale, color: palette.Primary, glyph: '█'},
		{weight: agent * scale, color: palette.Secondary, glyph: '█'},
		{weight: instruction * scale, color: palette.Info, glyph: '█'},
		{weight: other * scale, color: palette.Tool, glyph: '█'},
		{weight: free, color: palette.Muted, glyph: '░'},
	}, width), width)
}

// contextRoleLegend labels the role bar with per-role message counts,
// matching the bar's colours. A second line surfaces skill / agent / instruction content
// only when present so the common case stays one line. Returns one row per
// line so the caller can draw each on its own screen row.
func contextRoleLegend(s *RunState) []string {
	swatch := func(c theme.Color, label string, n int) string {
		return c.On("█") + palette.Subtle.On(" "+label+" "+itoa(n))
	}
	out := []string{
		swatch(palette.System, "sys", s.ctxSysMsgs) + " " +
			swatch(palette.User, "usr", s.ctxUserMsgs) + " " +
			swatch(palette.Assistant, "asst", s.ctxAsstMsgs) + " " +
			swatch(palette.Tool, "tool", s.ctxToolMsgs) + " " +
			palette.Muted.On("░ free"),
	}
	if s.ctxSkillBytes > 0 || s.ctxAgentBytes > 0 || s.ctxInstructionBytes > 0 {
		out = append(out,
			palette.Primary.On("█")+palette.Subtle.On(" skills")+"  "+
				palette.Secondary.On("█")+palette.Subtle.On(" agents")+"  "+
				palette.Info.On("█")+palette.Subtle.On(" instructions")+
				palette.Muted.On("  (of tool)"))
	}
	return out
}

// cacheLine is the compact "cached N (P%)" footnote shown beneath the role
// graph so the prompt-cache share stays visible without a second bar.
func cacheLine(s *RunState) string {
	pct := 0
	if s.lastIn > 0 {
		pct = s.lastCached * 100 / s.lastIn
	}
	return palette.Subtle.On("cached ") + palette.Fg.On(fmtCount(s.lastCached)) +
		palette.Muted.On(" ("+itoa(pct)+"%)")
}

// contextSplitBar paints the context window as cached | fresh | free so the
// reader sees at a glance how much of the budget the prompt cache is serving.
func contextSplitBar(s *RunState, width int) string {
	used := s.effectiveUsed()
	cached := float64(s.lastCached)
	fresh := float64(used - s.lastCached)
	if fresh < 0 {
		fresh = 0
	}
	free := float64(s.window - used)
	if free < 0 {
		free = 0
	}
	return s.markThresholdBar(stackedBar([]barSeg{
		{weight: cached, color: palette.Primary, glyph: '█'},
		{weight: fresh, color: palette.Assistant, glyph: '█'},
		{weight: free, color: palette.Muted, glyph: '░'},
	}, width), width)
}

// contextSplitLegend labels the cached/fresh/free bar with counts.
func contextSplitLegend(s *RunState) string {
	fresh := max(s.effectiveUsed()-s.lastCached, 0)
	return palette.Primary.On("█") + palette.Subtle.On(" cached "+fmtCount(s.lastCached)) +
		palette.Muted.On("  ") +
		palette.Assistant.On("█") + palette.Subtle.On(" fresh "+fmtCount(fresh)) +
		palette.Muted.On("  ") +
		palette.Muted.On("░ free")
}

// compactionSummary is the one-line COMPACTION row.
func (s *RunState) compactionSummary() string {
	parts := []string{}
	if s.compactions > 0 {
		parts = append(parts, "↯"+itoa(s.compactions))
	}
	parts = append(parts, itoa(s.compactBefore)+" → "+itoa(s.compactAfter)+" msgs")
	if s.compactBytes > 0 {
		parts = append(parts, fmtBytes(s.compactBytes)+" reclaimed")
	}
	if s.compactEngine != "" {
		parts = append(parts, s.compactEngine)
	}
	if !s.compactAt.IsZero() {
		parts = append(parts, fmtAgo(time.Since(s.compactAt)))
	}
	return palette.Subtle.On(strings.Join(parts, " · "))
}

func (s *RunState) compactionLines() []string {
	if s.compactions <= 0 {
		return nil
	}
	first := palette.Subtle.On("last ") + palette.Fg.On(itoa(s.compactBefore)+" → "+itoa(s.compactAfter)+" msgs")
	if s.compactBytes > 0 {
		first += palette.Muted.On(" · saved ") + palette.Success.On(fmtBytes(s.compactBytes))
	}
	second := palette.Subtle.On("engine ") + palette.Fg.On(s.compactEngine)
	if s.compactEngine == "" {
		second = palette.Subtle.On("engine unknown")
	}
	second += palette.Muted.On(" · ") + palette.Fg.On(itoa(s.compactions)+" total")
	if !s.compactAt.IsZero() {
		second += palette.Muted.On(" · ") + palette.Subtle.On(fmtAgo(time.Since(s.compactAt)))
	}
	out := []string{first, second}
	if threshold, remaining, ok := s.compactionThreshold(); ok {
		if remaining == 0 {
			out = append(out, palette.Warning.On("next compaction due")+
				palette.Muted.On(" · threshold ")+palette.Subtle.On(fmtCount(threshold)))
		} else {
			out = append(out, palette.Subtle.On("next in ")+palette.Fg.On(fmtCount(remaining))+
				palette.Muted.On(" · threshold ")+palette.Subtle.On(fmtCount(threshold)))
		}
	}
	return out
}

// lastTurnSummary is the last-turn token flow: in · out · iters · duration.
func (s *RunState) lastTurnSummary() string {
	parts := []string{
		palette.Subtle.On("in ") + palette.Fg.On(fmtCount(s.lastIn)),
		palette.Subtle.On("out ") + palette.Fg.On(fmtCount(s.lastOut)),
	}
	if s.lastIter > 0 {
		parts = append(parts, palette.Fg.On(itoa(s.lastIter)+" iter"))
	}
	if s.lastDuration > 0 {
		parts = append(parts, palette.Fg.On(fmtDuration(s.lastDuration)))
	}
	return strings.Join(parts, palette.Muted.On(" · "))
}

// throughputLine shows output tok/s with a recent-throughput sparkline and
// the cache-hit rate for the last turn.
func (s *RunState) throughputLine(_ int) string {
	var b strings.Builder
	tps := s.tokPerSec()
	b.WriteString(palette.Fg.On(itoa(int(tps+0.5)) + " tok/s"))
	if tps > 0 && len(s.history) >= 2 {
		b.WriteString(" " + sparkline(s.tpsSeries(), 8, 0, palette.Info, "", nil))
	}
	if s.lastCached > 0 && s.lastIn > 0 {
		pct := s.lastCached * 100 / s.lastIn
		b.WriteString(palette.Muted.On(" · ") + palette.Success.On("cache "+itoa(pct)+"%"))
	}
	return b.String()
}

// costSummary is the headline cost row: this turn · session · burn rate.
func (s *RunState) costSummary() string {
	parts := []string{
		palette.Warning.On("base-rate estimate"),
		palette.Subtle.On("turn ") + palette.Fg.On(fmtUSD(s.turnCost())),
		palette.Subtle.On("session ") + palette.Fg.On(fmtUSD(s.sessionCost())),
	}
	if s.hasSubAgentUsage() {
		parts = append(parts,
			palette.Subtle.On("parent ")+palette.Fg.On(fmtUSD(s.sessionCostParent())),
		)
	}
	if br := s.burnRate(); br > 0 {
		parts = append(parts, palette.Muted.On("~"+fmtUSD(br)+"/hr"))
	}
	return strings.Join(parts, palette.Muted.On(" · "))
}

func (s *RunState) toolsSummaryLine() string {
	parts := []string{palette.Subtle.On(itoa(s.sessionToolCalls) + " calls")}
	if n := len(s.toolStats); n > 0 {
		parts = append(parts, palette.Fg.On(itoa(n)+" tools"))
	}
	if s.toolsRunning > 0 {
		parts = append(parts, palette.Success.On(itoa(s.toolsRunning)+" running"))
	}
	fails := 0
	for _, st := range s.toolStats {
		fails += st.fails
	}
	if fails > 0 {
		parts = append(parts, palette.Warning.On(itoa(fails)+" failed"))
	}
	return strings.Join(parts, palette.Muted.On(" · "))
}

func (s *RunState) sessionTotalsLine() string {
	parts := make([]string, 0, 3)
	if s.sessionTurns > 0 {
		parts = append(parts, palette.Fg.On(fmtCount(s.sessionTurns))+palette.Subtle.On(" turns"))
	}
	if s.sessionToolCalls > 0 {
		parts = append(parts, palette.Fg.On(fmtCount(s.sessionToolCalls))+palette.Subtle.On(" calls"))
	}
	if s.hasPricing() || s.hasSessionCost() {
		parts = append(parts, palette.Fg.On(fmtUSD(s.sessionCost()))+palette.Subtle.On(" base-rate estimate"))
	}
	if s.unpricedCacheWriteTasks > 0 {
		parts = append(parts, palette.Warning.On(itoa(s.unpricedCacheWriteTasks)+" tasks: cache-write rate unknown (cost estimate incomplete)"))
	}
	if s.unpricedTasks > 0 {
		parts = append(parts, palette.Warning.On(itoa(s.unpricedTasks)+" tasks: rate unknown (cost incomplete)"))
	}
	return strings.Join(parts, palette.Muted.On(" · "))
}

// billingLine describes how the active backend is accounted for. Prices are
// stored per 1k tokens internally; the sidebar renders the more legible per-1M
// rate used by provider price sheets.
func (s *RunState) billingLine() string {
	switch {
	case s.local:
		return palette.Muted.On("local backend · unmetered")
	case s.subscription:
		return palette.Muted.On("subscription backend · unmetered")
	case s.hasPricing():
		return palette.Fg.On(fmtUSD(s.inCostPer1k*1000)+"/M input") +
			palette.Muted.On(" · ") +
			palette.Fg.On(fmtUSD(s.outCostPer1k*1000)+"/M output")
	default:
		return palette.Muted.On("metered · rate unknown")
	}
}

// legacyCostLine preserves persisted pre-versioning amounts without treating
// them as current spend or including them in burn-rate calculations.
func (s *RunState) legacyCostLine() string {
	line := palette.Warning.On("legacy historical cost ") + palette.Fg.On(fmtUSD(s.legacyCostUSD))
	if s.legacyCostParentUSD > 0 && s.legacyCostParentUSD != s.legacyCostUSD {
		line += palette.Muted.On(" · parent ") + palette.Fg.On(fmtUSD(s.legacyCostParentUSD))
	}
	return line
}

// legacyCacheSavingsLine preserves a persisted historical estimate. New usage
// never adds to it because cache rates are absent from model metadata.
func (s *RunState) legacyCacheSavingsLine() string {
	return palette.Warning.On("legacy cache-savings estimate ") + palette.Fg.On(fmtUSD(s.legacyCacheSavedUSD))
}

func (s *RunState) hasCacheReporting() bool {
	return len(s.cacheTargets) > 0 || s.cacheDiagnosticsLegacy
}

// cacheUsageLines reports target attribution, known base-cost subtotal, and
// explicit coverage gaps without repeating the aggregate cache token rows.
func (s *RunState) cacheUsageLines(_ int) []string {
	targets := sortedCacheTargets(s.cacheTargets)
	lines := make([]string, 0, len(targets)*2+1)
	for _, d := range targets {
		label := d.Provider + "/" + d.Model
		if d.Provider == "" && d.Model == "" {
			label = "unknown target"
		}
		head := palette.Fg.On(label) + palette.Muted.On(" · "+d.BillingClass+" · ") +
			palette.Subtle.On(itoa(d.SettledTasks)+" settled")
		if d.BillingClass == billingClassLocal || d.BillingClass == billingClassSubscription {
			head += palette.Muted.On(" · unmetered")
		} else {
			head += palette.Subtle.On(" · base-rate estimate ") + palette.Fg.On(fmtUSD(d.KnownCostUSD))
			if d.BaseRateUnknownTasks > 0 {
				head += palette.Warning.On(" · base rate unknown " + itoa(d.BaseRateUnknownTasks))
			}
		}
		lines = append(lines, head)

		usage := palette.Subtle.On("cache read ") + palette.Fg.On(fmtCount(d.CacheReadTokens))
		if d.CacheReadShareKnown {
			usage += palette.Muted.On(" (" + fmt.Sprintf("%.0f", d.CacheReadShare*100) + "%)")
		}
		usage += palette.Muted.On(" · create ") + palette.Fg.On(fmtCount(d.CacheCreationTokens))
		coverage := make([]string, 0, 5)
		if d.PricingTierUnknownTasks > 0 {
			coverage = append(coverage, "prompt pricing tier unavailable "+itoa(d.PricingTierUnknownTasks)+" (base-rate estimate incomplete)")
		}
		if d.CacheReadRateUnknownTasks > 0 {
			coverage = append(coverage, "read rate unknown "+itoa(d.CacheReadRateUnknownTasks))
		}
		if d.CacheWriteRateUnknownTasks > 0 {
			coverage = append(coverage, "write rate unknown "+itoa(d.CacheWriteRateUnknownTasks))
		}
		if d.MissingUsageTasks > 0 {
			coverage = append(coverage, "usage absent "+itoa(d.MissingUsageTasks))
		}
		if d.ReportedZeroUsageTasks > 0 {
			coverage = append(coverage, "reported zero "+itoa(d.ReportedZeroUsageTasks))
		}
		if len(coverage) > 0 {
			usage += palette.Warning.On(" · " + strings.Join(coverage, ", "))
		}
		lines = append(lines, usage)
	}
	if s.cacheDiagnosticsLegacy {
		lines = append(lines, palette.Warning.On("legacy history: target attribution unavailable"))
	}
	return lines
}

// sectionHead keeps a single plain heading row; callers retain their own spacing.
func sectionHead(label string, width int) string {
	return terminalChrome().PaneTitle(strings.ToLower(label), width, false)
}

// contextHeadline is the dense one-row context summary: percent-full
// (pressure-coloured) · used / window · cached N (P%). The cache piece drops
// out when absent. Fill-over-time is deliberately NOT shown here — the
// composition bar directly below already shows how full the window is, so a
// fill sparkline would just duplicate it.
func (s *RunState) contextHeadline() string {
	line := s.contextSummary()
	if s.lastCached > 0 && s.lastIn > 0 {
		line += palette.Muted.On(" · ") + cacheLine(s)
	}
	return line
}

// labelledSpark renders "label <sparkline>" sized so the sparkline fills the
// remaining width after the label.
func labelledSpark(label string, vals []float64, width int, normMax float64, c, markC theme.Color, marks []bool) string {
	sw := max(width-ansi.StringWidth(label)-1, 1)
	return palette.Subtle.On(label) + " " + sparkline(vals, sw, normMax, c, markC, marks)
}

// toolHistogram renders up to max tool rows: name (padded) · calls · duration,
// with failures flagged in Warning.
func toolHistogram(tools []toolRow, _ int, maxRows int) []string {
	nameW := 0
	for i, t := range tools {
		if i >= maxRows {
			break
		}
		if w := ansi.StringWidth(t.name); w > nameW {
			nameW = w
		}
	}
	if nameW > 16 {
		nameW = 16
	}
	if nameW < 8 {
		nameW = 8
	}
	out := make([]string, 0, len(tools))
	for i, t := range tools {
		if i >= maxRows {
			break
		}
		name := t.name
		if ansi.StringWidth(name) > nameW {
			name = ansi.Truncate(name, nameW, "")
		}
		row := palette.Fg.On(padRight(name, nameW)) + palette.Muted.On("  "+itoa(t.calls)+"×")
		if t.fails > 0 {
			row += palette.Warning.On(" (" + itoa(t.fails) + "✗)")
		}
		row += palette.Muted.On(" · " + fmtDuration(t.dur))
		out = append(out, row)
	}
	return out
}

// pressureColor maps a fill fraction to the cool→amber→red pressure tone.
func pressureColor(frac float64) theme.Color {
	switch {
	case frac >= 0.95:
		return palette.Error
	case frac >= 0.80:
		return palette.Warning
	default:
		return palette.Primary
	}
}
