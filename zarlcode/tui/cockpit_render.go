package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

// cockpitMinWidth / cockpitMinHeight are the inner dimensions below which
// the rich graphical cockpit degrades to the flat row fallback (run.lines).
const (
	cockpitMinWidth  = 26
	cockpitMinHeight = 10
)

// cockpitLines renders the rich cockpit body — gauge, context graph,
// sparklines, last-turn flow, cost, and the tool histogram — as a slice of
// ANSI-styled rows sized to width. drawSidebar draws them clipped to the
// pane; the dashboard reuses the same builders at a larger width.
//
// width is the inner content width (box interior). pulse breathes the gauge
// leading edge while a turn is in flight.
func (m *UI) cockpitLines(width int) []string {
	s := &m.session.Run

	var out []string
	add := func(ss ...string) { out = append(out, ss...) }

	// --- LLM/session overview ---------------------------------------------
	add(m.llmStateLines()...)

	// Nothing run and nothing running — show just the LLM/session overview above.
	if !s.Running && len(s.history) == 0 && s.liveCtx == 0 {
		return out
	}
	add("")

	// --- context -------------------------------------------------------
	// Keep the context composition graph as the primary visual: it is the fastest
	// way to understand what is occupying the window. The surrounding text carries
	// pressure / compaction hints so the graph itself can stay clean.
	add(sectionHead("context", width))
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
		add(sectionHead("compaction", width))
		add(s.compactionLines()...)
	}

	// --- LAST TURN -----------------------------------------------------
	if s.lastTotal > 0 || s.lastIn > 0 {
		add(sectionHead("last turn", width))
		add(s.lastTurnSummary())
		add(s.throughputLine(width))
	}

	// --- COST ----------------------------------------------------------
	add(sectionHead("cost", width))
	switch {
	case s.hasPricing():
		add(s.costSummary())
		if s.sessionCached > 0 {
			add(s.cacheSavedLine())
		}
	case s.subscription:
		add(palette.Muted.On("subscription — no metered cost"))
	case s.local:
		add(palette.Muted.On("local — no metered cost"))
	default:
		add(palette.Muted.On("metered · rate unknown"))
	}

	// --- TOOLS ---------------------------------------------------------
	if tools := s.topTools(); len(tools) > 0 {
		add(sectionHead("tools", width))
		add(toolHistogram(tools, width, 12)...)
	}

	return out
}

// llmStateLines renders the sidebar's top card. This is deliberately not a
// pile of abbreviations: the side pane should be a useful at-a-glance overview
// of the active LLM configuration before the live token/cost/tool gauges begin.
func (m *UI) llmStateLines() []string {
	s := &m.session.Run
	out := make([]string, 0, 6)
	add := func(label, value string) {
		out = append(out, palette.Subtle.On(padRight(label, 10))+value)
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

	add("billing", s.billingLine())

	if m.session.Workspace != "" {
		ws := palette.Fg.On(m.session.Workspace)
		if m.session.Branch != "" {
			ws += palette.Muted.On(" · ") + palette.Secondary.On(m.session.Branch)
		}
		add("workspace", ws)
	}
	if pr := m.session.PR; pr != nil {
		add("pr", prLine(pr))
	}
	if !m.session.StartedAt.IsZero() {
		started := palette.Fg.On(m.session.StartedAt.Format("15:04")) +
			palette.Muted.On(" · ") + palette.Subtle.On(fmtAgo(time.Since(m.session.StartedAt)))
		add("started", started)
	}

	return out
}

// cockpitStatusLine is the one-line "what's happening now" row: a braille
// activity indicator, the run state, and the live iteration / tool counters.
func (m *UI) cockpitStatusLine() string {
	s := &m.session.Run
	glyph, label := palette.Muted.On(runActivityGlyph(m.frame, false)), palette.Muted.On("idle")
	if s.Running {
		glyph, label = palette.Success.On(runActivityGlyph(m.frame, true)), palette.Success.On("running")
	}
	parts := []string{glyph + " " + label}
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
		free := s.window - used
		if free < 0 {
			free = 0
		}
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
	remaining = threshold - s.effectiveUsed()
	if remaining < 0 {
		remaining = 0
	}
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
// split into load_skill (skills) / spawn_agent (agents) / other tool output,
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
	other := tool - skill - agent
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
		{weight: other * scale, color: palette.Tool, glyph: '█'},
		{weight: free, color: palette.Muted, glyph: '░'},
	}, width), width)
}

// contextRoleLegend labels the role bar with per-role message counts,
// matching the bar's colours. A second line surfaces skill / agent content
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
	if s.ctxSkillBytes > 0 || s.ctxAgentBytes > 0 {
		out = append(out,
			palette.Primary.On("█")+palette.Subtle.On(" skills")+"  "+
				palette.Secondary.On("█")+palette.Subtle.On(" agents")+
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
	fresh := s.effectiveUsed() - s.lastCached
	if fresh < 0 {
		fresh = 0
	}
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

// cacheSavedLine shows the dollars the prompt cache saved this session and
// that saving as a share of what the session would otherwise have cost.
func (s *RunState) cacheSavedLine() string {
	saved := s.cacheSaved()
	gross := s.sessionCost() + saved
	pct := 0
	if gross > 0 {
		pct = int(saved / gross * 100)
	}
	return palette.Success.On("cache saved "+fmtUSD(saved)) +
		palette.Muted.On(" ("+itoa(pct)+"% of spend)")
}

// sectionHead renders a section header rule that joins into the left border:
// ├─[label]────────────────────────────────
func sectionHead(label string, width int) string {
	head := bracketed(palette.Primary.On(strings.ToLower(label)))
	fill := width - ansi.StringWidth(label) - 4 // ├─[ + label + ]
	if fill < 0 {
		fill = 0
	}
	return palette.Border.On("├─") + head + palette.Border.On(strings.Repeat("─", fill))
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
	sw := width - ansi.StringWidth(label) - 1
	if sw < 1 {
		sw = 1
	}
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
