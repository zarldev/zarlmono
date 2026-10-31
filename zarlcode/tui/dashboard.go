package tui

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// dashboardRect is the middle band (timeline + sidebar combined) the
// expanded cockpit dashboard takes over — full width, between the header and
// the editor.
func (m *UI) dashboardRect() uv.Rectangle {
	return uv.Rect(0, m.layout.main.Min.Y, m.width, m.layout.main.Dy())
}

// dashColGap is the blank gutter between dashboard columns.
const (
	cockpitMinWidth  = 26
	cockpitMinHeight = 10
	dashColGap       = 3
)

// drawDashboard paints the detailed session views on the same quiet surface as
// the main screen. The overview uses up to three columns as space permits.
func (m *UI) drawDashboard(scr uv.Screen, r uv.Rectangle) {
	innerW, contentH := dashboardContentSize(r)
	if innerW < cockpitMinWidth || contentH < 1 {
		return
	}
	l := drawDetailView(scr, r, "session", contextViewTabNames, int(m.contextView.tab))
	x0, contentY := l.Body.Min.X, l.Body.Min.Y
	cw := innerW
	contentLines := m.activeContextViewLines(cw)
	maxScroll := max(len(contentLines)-contentH, 0)
	if m.contextView.activeScroll() > maxScroll {
		m.contextView.setActiveScroll(maxScroll)
	}
	if m.contextView.activeScroll() < 0 {
		m.contextView.setActiveScroll(0)
	}
	scroll := m.contextView.activeScroll()
	hints := "esc close · ←→ tabs"
	if innerW >= 64 {
		hints = "Ctrl+L / esc close · ←→ / tab views · ↑↓ scroll"
	}
	if m.contextView.tab == contextViewTabPrompt || m.contextView.tab == contextViewTabTools {
		hints = "esc close · ←→ tabs · ↑↓ scroll · r refresh snapshot"
	}
	position := ""
	if maxScroll > 0 {
		position = helpScrollPosition(scroll, contentH, len(contentLines))
	}
	drawDetailFooter(scr, l.Footer, hints, position)
	for i := scroll; i < len(contentLines) && i-scroll < contentH; i++ {
		drawLine(scr, uv.Rect(x0, contentY+i-scroll, cw, 1), ansi.Truncate(contentLines[i], cw, ""))
	}
	drawPaneScrollbar(scr, r.Max.X-2, contentY, contentH, len(contentLines), scroll)
}

func dashboardColumnCount(innerW int) int {
	switch {
	case innerW >= 3*cockpitMinWidth+2*dashColGap:
		return 3
	case innerW >= 2*cockpitMinWidth+dashColGap:
		return 2
	default:
		return 1
	}
}

func (m *UI) dashboardGeometry() (int, int) {
	return dashboardContentSize(m.dashboardRect())
}

// Drawing, scroll limits and paging must measure the same full-width content.
func dashboardContentSize(r uv.Rectangle) (int, int) {
	l := detailViewGeometry(r)
	return max(l.Body.Dx(), 1), l.Body.Dy()
}

func (m *UI) dashboardMaxScroll() int {
	colW, visibleH := m.dashboardGeometry()
	if visibleH <= 0 {
		return 0
	}
	contentLines := m.activeContextViewLines(colW)
	if len(contentLines) <= visibleH {
		return 0
	}
	return len(contentLines) - visibleH
}

func (m *UI) clampContextViewScroll() {
	maxScroll := m.dashboardMaxScroll()
	if m.contextView.activeScroll() > maxScroll {
		m.contextView.setActiveScroll(maxScroll)
	}
	if m.contextView.activeScroll() < 0 {
		m.contextView.setActiveScroll(0)
	}
}

func (m *UI) dashboardPageStep() int {
	_, visibleH := m.dashboardGeometry()
	if visibleH <= 2 {
		return 1
	}
	return visibleH - 2
}

func (m *UI) activeContextViewLines(width int) []string {
	switch m.contextView.tab {
	case contextViewTabContext:
		return wrapDashboardLines(m.contextPaneLines(width, 0), width)
	case contextViewTabPrompt:
		view := m.contextView.inspection
		if view == nil || !view.ready {
			return m.contextPromptLines(width)
		}
		if view.prompt.lines == nil || view.prompt.width != width || view.prompt.theme != themeGen {
			view.prompt = inspectionLines{width: width, theme: themeGen, lines: wrapDashboardLines(m.contextPromptLines(width), width)}
		}
		return view.prompt.lines
	case contextViewTabTools:
		return wrapDashboardLines(m.contextToolsLines(width), width)
	case contextViewTabEvents:
		return wrapDashboardLines(m.contextEventsLines(width), width)
	default:
		cols := dashboardColumnCount(width)
		colW := (width - (cols-1)*dashColGap) / cols
		columns := m.dashboardColumns(cols, colW)
		for i, lines := range columns {
			columns[i] = wrapDashboardLines(lines, colW)
		}
		out := flattenDashboardColumns(columns, colW)
		if cols > 1 {
			out = append(out, "", sidebarHeading("session"))
			out = append(out, wrapDashboardLines(m.llmStateLines(), width)...)
		}
		return out
	}
}

// Retain complete metadata and cost qualifiers instead of clipping column rows.
func wrapDashboardLines(lines []string, width int) []string {
	var out []string
	for _, line := range lines {
		out = append(out, strings.Split(ansi.Hardwrap(line, width, true), "\n")...)
	}
	return out
}

func flattenDashboardColumns(columns [][]string, colW int) []string {
	maxLines := 0
	for _, lines := range columns {
		if len(lines) > maxLines {
			maxLines = len(lines)
		}
	}
	out := make([]string, 0, maxLines)
	for row := range maxLines {
		parts := make([]string, 0, len(columns))
		for _, lines := range columns {
			if row < len(lines) {
				parts = append(parts, padStyled(lines[row], colW))
			} else {
				parts = append(parts, spaces(colW))
			}
		}
		out = append(out, strings.Join(parts, strings.Repeat(" ", dashColGap)))
	}
	return out
}

// dashboardColumns lays the cockpit sections into cols columns of width colW.
// Three columns: context | flow+signals | tools+history. Two: context+flow |
// signals+tools. One: the detailed session overview stacked vertically.
func (m *UI) dashboardColumns(cols, colW int) [][]string {
	switch cols {
	case 3:
		mid := append(m.flowColumn(colW), "")
		mid = append(mid, m.signalsColumn(colW)...)
		return [][]string{m.ctxColumn(colW), mid, m.toolsColumn(colW)}
	case 2:
		left := append(m.ctxColumn(colW), "")
		left = append(left, m.flowColumn(colW)...)
		right := append(m.signalsColumn(colW), "")
		right = append(right, m.toolsColumn(colW)...)
		return [][]string{left, right}
	default:
		return [][]string{m.cockpitLines(colW)}
	}
}

// ctxColumn is the dashboard's context column: status, the pressure headline,
// a full-width gauge, the cached/fresh/free split, the fill sparkline, and
// the last compaction.
func (m *UI) ctxColumn(width int) []string {
	s := &m.session.Run
	out := []string{m.cockpitStatusLine()}
	// Context section only once there's real usage — no empty gauges.
	if s.window > 0 && s.liveCtx > 0 {
		out = append(out, "", sidebarHeading("context"), s.contextSummary())
		// A single composition bar fills with role colours (used) + ░ free.
		// Role graph when a breakdown is in, plus a dedicated CACHE split bar
		// (the dashboard has the room); otherwise the split stands in.
		if s.hasBreakdown() {
			out = append(out, contextRoleBar(s, width), "")
			out = append(out, contextRoleLegend(s)...)
			out = append(out, "", sidebarHeading("cache"), contextSplitBar(s, width), "", contextSplitLegend(s))
		} else {
			out = append(out, contextSplitBar(s, width), "", contextSplitLegend(s))
		}
	}
	if s.compactions > 0 {
		out = append(out, "", sidebarHeading("compaction"), s.compactionSummary())
	}
	return out
}

// flowColumn is the last-turn flow plus the cost breakdown.
func (m *UI) flowColumn(width int) []string {
	s := &m.session.Run
	var out []string
	// Last-turn section only when a turn has completed — no placeholder.
	if s.lastTotal > 0 || s.lastIn > 0 {
		out = append(out, sidebarHeading("last turn"), s.lastTurnSummary())
		if tp := s.throughputLine(width); tp != "" {
			out = append(out, tp)
		}
		out = append(out, "")
	}
	out = append(out, sidebarHeading("cost"))
	switch {
	case s.hasPricing() || s.hasSessionCost():
		out = append(out, s.costSummary())
	case s.subscription:
		out = append(out, palette.Muted.On("subscription — no metered cost"))
	case s.local:
		out = append(out, palette.Muted.On("local — no metered cost"))
	default:
		out = append(out, palette.Muted.On("metered · rate unknown"))
	}
	if s.hasLegacyCost() {
		out = append(out, s.legacyCostLine())
	}
	if s.legacyCacheSavedUSD > 0 {
		out = append(out, s.legacyCacheSavingsLine())
	}
	if s.hasCacheReporting() {
		out = append(out, s.cacheUsageLines(width)...)
	}
	return out
}

// signalsColumn is the sparkline grid — one labelled trend per metric that
// has no bar of its own (fill lives in the composition bar, so it's not
// repeated here): throughput, cache-hit rate, and per-turn cost.
func (m *UI) signalsColumn(width int) []string {
	s := &m.session.Run
	out := []string{sidebarHeading("signals")}
	if len(s.history) < 2 {
		return append(out, palette.Muted.On("(collecting — needs 2+ turns)"))
	}
	return append(out,
		labelledSpark("tok/s", s.tpsSeries(), width, 0, palette.Info, "", nil),
		labelledSpark("cache", s.cacheSeries(), width, 1, palette.Success, "", nil),
		labelledSpark("cost ", s.costSeries(), width, 0, palette.Warning, "", nil),
	)
}

// toolsColumn is the session tool histogram plus the per-turn history table.
func (m *UI) toolsColumn(width int) []string {
	s := &m.session.Run
	var out []string
	if tools := s.topTools(); len(tools) > 0 {
		out = append(out, sidebarHeading("tools"))
		out = append(out, toolHistogram(tools, width, 12)...)
	}
	if len(s.history) > 0 {
		out = append(out, "", sidebarHeading("history"))
		out = append(out, s.historyTable(12)...)
	}
	return out
}

// historyTable renders the last n turns as a compact aligned table:
// turn · in · out · cache% · cost, with a ↯ on turns where a compaction
// landed.
func (s *RunState) historyTable(n int) []string {
	header := "  " + padLeft("#", 2) + " " + padLeft("in", 5) + " " +
		padLeft("out", 5) + " " + padLeft("cache", 5) + " " + padLeft("$", 6)
	out := []string{palette.Subtle.On(header)}

	start := 0
	if len(s.history) > n {
		start = len(s.history) - n
	}
	for i := start; i < len(s.history); i++ {
		h := s.history[i]
		cachePct := 0
		if h.tokIn > 0 {
			cachePct = h.cached * 100 / h.tokIn
		}
		// Colour the mark and the numeric body separately so a compaction
		// flag's reset doesn't bleed the row's colour.
		mark := " "
		if h.compacted {
			mark = palette.Warning.On("↯")
		}
		body := padLeft(itoa(i+1), 2) + " " +
			padLeft(fmtCount(h.tokIn), 5) + " " +
			padLeft(fmtCount(h.tokOut), 5) + " " +
			padLeft(itoa(cachePct)+"%", 5) + " " +
			padLeft(fmtUSD(h.costUSD), 6)
		out = append(out, mark+" "+palette.Fg.On(body))
	}
	return out
}

// padLeft right-aligns s in n display columns.
func padLeft(s string, n int) string {
	w := ansi.StringWidth(s)
	if w >= n {
		return s
	}
	return spaces(n-w) + s
}
