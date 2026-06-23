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

// drawDashboard paints the full-width context dashboard: the same cockpit
// signals as the sidebar, spread across two or three columns with room for
// the extra sparkline grid and per-turn history table that don't fit the
// 48-col sidebar. Degrades to a single column on narrower terminals.
func (m *UI) drawDashboard(scr uv.Screen, r uv.Rectangle) {
	drawFrame(scr, r, frameStyle{Label: "context view", Border: palette.Border, LabelColor: palette.Primary})
	innerW, innerH := r.Dx()-4, r.Dy()-2
	if innerW < cockpitMinWidth || innerH < 4 {
		return
	}
	x0, y0 := r.Min.X+2, r.Min.Y+1

	tabRow := m.contextViewTabBar(innerW)
	drawLine(scr, uv.Rect(x0, y0, innerW, 1), tabRow)
	if tabHelp := m.contextViewTabHelp(innerW); tabHelp != "" {
		drawLine(scr, uv.Rect(x0, y0+1, innerW, 1), tabHelp)
	}
	drawLine(scr, uv.Rect(x0, y0+2, innerW, 1), palette.Border.On(strings.Repeat("─", innerW)))

	contentY := y0 + 3
	contentH := innerH - 4
	if contentH < 1 {
		contentH = 1
	}
	cw := innerW
	contentLines := m.activeContextViewLines(cw)
	m.clampContextViewScroll()
	scroll := m.contextView.activeScroll()
	if maxScroll := m.dashboardMaxScroll(); maxScroll > 0 {
		indicator := palette.Subtle.On(" ↑↓ scroll ") + palette.Fg.On(itoa(scroll+1)) + palette.Subtle.On("/") + palette.Fg.On(itoa(maxScroll+1))
		indicatorW := ansi.StringWidth(indicator)
		drawLine(scr, uv.Rect(max(r.Min.X+1, r.Max.X-indicatorW-2), r.Min.Y, indicatorW, 1), indicator)
	}
	for i := scroll; i < len(contentLines) && i-scroll < contentH; i++ {
		drawLine(scr, uv.Rect(x0, contentY+i-scroll, cw, 1), ansi.Truncate(contentLines[i], cw, ""))
	}
	drawPaneScrollbar(scr, r.Max.X-2, contentY, contentH, len(contentLines), scroll)
	drawLine(scr, uv.Rect(x0, r.Max.Y-3, innerW, 1), palette.Border.On(strings.Repeat("─", innerW)))
	footer := keyLegend(
		keyHint{"tab/←→", "switch tab"},
		keyHint{"↑↓/jk", "scroll"},
		keyHint{"pgup/pgdn", "page"},
		keyHint{"esc", "close"},
	)
	drawPaneRow(scr, uv.Rect(x0, r.Max.Y-2, innerW, 1), palette.Subtle.On(" "+footer), "")
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

func dashboardMaxScroll(columns [][]string, visibleH int) int {
	maxLines := 0
	for _, lines := range columns {
		if len(lines) > maxLines {
			maxLines = len(lines)
		}
	}
	if visibleH <= 0 || maxLines <= visibleH {
		return 0
	}
	return maxLines - visibleH
}

func (m *UI) dashboardGeometry() (int, int) {
	r := m.dashboardRect()
	innerW, innerH := r.Dx()-4, r.Dy()-2
	if innerW < cockpitMinWidth || innerH < 1 {
		return cockpitMinWidth, 0
	}
	cols := dashboardColumnCount(innerW)
	colW := (innerW - (cols-1)*dashColGap) / cols
	return colW, innerH
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

func (m *UI) clampDashboardScroll() {
	m.clampContextViewScroll()
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
		return m.contextPaneLines(width, 0)
	case contextViewTabPrompt:
		return m.contextPromptLines(width)
	case contextViewTabTools:
		return m.contextToolsLines(width)
	case contextViewTabEvents:
		return m.contextEventsLines(width)
	default:
		cols := dashboardColumnCount(width)
		colW := (width - (cols-1)*dashColGap) / cols
		return flattenDashboardColumns(m.dashboardColumns(cols, colW), colW)
	}
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

func (m *UI) contextViewTabBar(width int) string {
	parts := make([]string, len(contextViewTabNames))
	for i, name := range contextViewTabNames {
		if contextViewTab(i) == m.contextView.tab {
			parts[i] = palette.Primary.On("[ " + name + " ]")
		} else {
			parts[i] = palette.Subtle.On(name)
		}
	}
	return ansi.Truncate(strings.Join(parts, "  "), width, "")
}

func (m *UI) contextViewTabHelp(width int) string {
	summary := "live context surface · summary-first prompt · grouped tools/events"
	return palette.Muted.On(ansi.Truncate(summary, width, ""))
}

// dashboardColumns lays the cockpit sections into cols columns of width colW.
// Three columns: context | flow+signals | tools+history. Two: context+flow |
// signals+tools. One: the sidebar body verbatim.
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
		out = append(out, "", bracketed(palette.Primary.On("context")), s.contextSummary())
		// A single composition bar fills with role colours (used) + ░ free.
		// Role graph when a breakdown is in, plus a dedicated CACHE split bar
		// (the dashboard has the room); otherwise the split stands in.
		if s.hasBreakdown() {
			out = append(out, contextRoleBar(s, width), "")
			out = append(out, contextRoleLegend(s)...)
			out = append(out, "", bracketed(palette.Primary.On("cache")), contextSplitBar(s, width), "", contextSplitLegend(s))
		} else {
			out = append(out, contextSplitBar(s, width), "", contextSplitLegend(s))
		}
	}
	if s.compactions > 0 {
		out = append(out, "", bracketed(palette.Primary.On("compaction")), s.compactionSummary())
	}
	return out
}

// flowColumn is the last-turn flow plus the cost breakdown.
func (m *UI) flowColumn(width int) []string {
	s := &m.session.Run
	var out []string
	// Last-turn section only when a turn has completed — no placeholder.
	if s.lastTotal > 0 || s.lastIn > 0 {
		out = append(out, bracketed(palette.Primary.On("last turn")), s.lastTurnSummary())
		if tp := s.throughputLine(width); tp != "" {
			out = append(out, tp)
		}
		out = append(out, "")
	}
	out = append(out, bracketed(palette.Primary.On("cost")))
	switch {
	case s.hasPricing():
		out = append(out, s.costSummary())
		if s.sessionCached > 0 {
			out = append(out, s.cacheSavedLine())
		}
	case s.subscription:
		out = append(out, palette.Muted.On("subscription — no metered cost"))
	case s.local:
		out = append(out, palette.Muted.On("local — no metered cost"))
	default:
		out = append(out, palette.Muted.On("metered · rate unknown"))
	}
	return out
}

// signalsColumn is the sparkline grid — one labelled trend per metric that
// has no bar of its own (fill lives in the composition bar, so it's not
// repeated here): throughput, cache-hit rate, and per-turn cost.
func (m *UI) signalsColumn(width int) []string {
	s := &m.session.Run
	out := []string{bracketed(palette.Primary.On("signals"))}
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
		out = append(out, bracketed(palette.Primary.On("tools")))
		out = append(out, toolHistogram(tools, width, 12)...)
	}
	if len(s.history) > 0 {
		out = append(out, "", bracketed(palette.Primary.On("history")))
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
