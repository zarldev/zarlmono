package tui

import (
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

// Draw paints the pane rectangles onto scr. area is the clip region
// (the full screen); per-pane geometry comes from m.layout.
func (m *UI) Draw(scr uv.Screen, _ uv.Rectangle) {
	// A full-screen overlay (the settings surface) owns the entire frame —
	// skip the panes + global status bar so there's one footer, not two.
	if m.overlay.active() && m.overlay.coversScreen() {
		m.overlay.draw(scr, uv.Rect(0, 0, m.width, m.height))
		return
	}
	if m.intro != nil {
		m.intro.provider = m.session.Provider
		m.intro.model = m.session.Model
		m.intro.draw(scr, uv.Rect(0, 0, m.width, m.height), m.session.PlanMode)
		if m.overlay.active() {
			m.overlay.draw(scr, uv.Rect(0, 0, m.width, m.height))
		}
		return
	}
	if m.startupFailure != nil {
		m.startupFailure.draw(scr, uv.Rect(0, 0, m.width, m.height))
		return
	}
	// The header rect is zero-height in the default layout; app/mode/model live
	// in the timeline title so there is only one top status row.
	m.headerPane.Draw(scr, m.layout.header)
	if m.session.CockpitExpanded {
		// The dashboard takes over the whole middle band (timeline +
		// sidebar); the composer and status bar stay put.
		m.drawDashboard(scr, m.dashboardRect())
	} else {
		m.drawTimeline(scr, m.layout.main)
		if !m.layout.sidebar.Empty() {
			m.drawSidebar(scr, m.layout.sidebar)
		}
	}
	m.composer.draw(scr, m.layout.editor, m.session.PlanMode)
	m.statusPane.Draw(scr, m.layout.status)

	// Dialogs draw last, centered over everything.
	if m.overlay.active() {
		m.overlay.draw(scr, uv.Rect(0, 0, m.width, m.height))
	}
}

// drawBox paints a single-line box-drawing border around r and writes
// label onto the top edge, in the default border/label colours.

// drawPaneFrame paints the standard zarlcode pane chrome and returns the
// drawable interior rectangle. Full-screen overlays use this instead of
// hand-rolled title bars so they match the main cockpit panes.

// drawPaneFrameColored is drawPaneFrame with explicit border + label colours.
func drawPaneFrameColored(scr uv.Screen, r uv.Rectangle, label string, border, labelCol theme.Color) uv.Rectangle {
	return drawFrame(scr, r, frameStyle{Label: label, Border: border, LabelColor: labelCol})
}

// splitPaneLayout is the standard full-screen master/detail overlay shape:
// an in-pane context row, a nav rail, a detail body, and an in-pane footer.
type splitPaneLayout struct {
	Inner   uv.Rectangle
	Context uv.Rectangle
	Body    uv.Rectangle
	Nav     uv.Rectangle
	Detail  uv.Rectangle
	Footer  uv.Rectangle
}

// drawSplitPane paints the standard split-pane chrome and returns named content
// regions for callers to fill. It is the shared base for file browser/settings/
// saved-plan style overlays.
func drawSplitPane(scr uv.Screen, area uv.Rectangle, label string, navW int) (splitPaneLayout, bool) {
	return drawSplitPaneColored(scr, area, label, navW, palette.Border, palette.Primary)
}

// drawSplitPaneColored is drawSplitPane with explicit border + label colours.
func drawSplitPaneColored(scr uv.Screen, area uv.Rectangle, label string, navW int, border, labelCol theme.Color) (splitPaneLayout, bool) {
	inner := drawPaneFrameColored(scr, area, label, border, labelCol)
	w, h := inner.Dx(), inner.Dy()
	if w < 12 || h < 5 {
		return splitPaneLayout{}, false
	}
	if navW > w/3 {
		navW = w / 3
	}
	if navW < 4 || w-navW-1 < 4 {
		return splitPaneLayout{}, false
	}

	x0, y0 := inner.Min.X, inner.Min.Y
	contextY := y0
	topRuleY := y0 + 1
	bodyY := y0 + 2
	footerY := inner.Max.Y - 1
	bottomRuleY := footerY - 1
	bodyH := bottomRuleY - bodyY
	if bodyH < 1 {
		return splitPaneLayout{}, false
	}

	drawPaneHRuleColored(scr, x0, topRuleY, w, navW, "┬", border)
	drawPaneHRuleColored(scr, x0, bottomRuleY, w, navW, "┴", border)
	for y := bodyY; y < bottomRuleY; y++ {
		drawLine(scr, uv.Rect(x0+navW, y, 1, 1), border.On("│"))
	}

	return splitPaneLayout{
		Inner:   inner,
		Context: uv.Rect(x0, contextY, w, 1),
		Body:    uv.Rect(x0, bodyY, w, bodyH),
		Nav:     uv.Rect(x0+1, bodyY, navW-1, bodyH),
		Detail:  uv.Rect(x0+navW+1, bodyY, w-navW-1, bodyH),
		Footer:  uv.Rect(x0, footerY, w, 1),
	}, true
}

// dialogPaneLayout is the single-column centered-dialog analog of
// splitPaneLayout: a top context row (tabs / summary), a body region (the
// scrollable list or editor), and a bottom footer row (key hints). All three
// carry one column of inner padding inside the frame, matching the historical
// innerX = r.Min.X+2 / innerW = r.Dx()-4 the picker dialogs hand-derived.
type dialogPaneLayout struct {
	Inner   uv.Rectangle
	Context uv.Rectangle
	Body    uv.Rectangle
	Footer  uv.Rectangle
}

// drawDialogPane paints a centered w×h bordered box and returns its named
// regions, so picker/editor dialogs stop re-deriving innerX/innerW/footerY by
// hand. ok is false when the box is too small to hold all three regions.
func drawDialogPane(scr uv.Screen, area uv.Rectangle, label string, w, h int, border, labelCol theme.Color) (dialogPaneLayout, bool) {
	r := centerRect(area, w, h)
	drawBoxColored(scr, r, label, border, labelCol)
	x := r.Min.X + 2
	cw := r.Dx() - 4
	if cw < 1 || r.Dy() < 4 {
		return dialogPaneLayout{}, false
	}
	ctxY := r.Min.Y + 1
	footerY := r.Min.Y + r.Dy() - 2
	bodyY := ctxY + 1
	bodyH := footerY - bodyY
	if bodyH < 1 {
		return dialogPaneLayout{}, false
	}
	return dialogPaneLayout{
		Inner:   r,
		Context: uv.Rect(x, ctxY, cw, 1),
		Body:    uv.Rect(x, bodyY, cw, bodyH),
		Footer:  uv.Rect(x, footerY, cw, 1),
	}, true
}

// drawPaneRow paints a left/right row inside an already-framed pane region.
func drawPaneRow(scr uv.Screen, r uv.Rectangle, left, right string) {
	if r.Dx() < 1 || r.Dy() < 1 {
		return
	}
	drawLine(scr, uv.Rect(r.Min.X, r.Min.Y, r.Dx(), 1), rowLayout(left, right, r.Dx()))
}

// drawListRow paints the standard selectable row used by full-screen split-pane
// nav rails. Focused selections get the primary marker and highlight fill;
// unfocused selections keep their place with the assistant marker only.
func drawListRow(scr uv.Screen, r uv.Rectangle, label string, selected, focused bool) {
	w := r.Dx()
	if w < 1 || r.Dy() < 1 {
		return
	}
	marker := "  "
	if selected {
		marker = palette.Assistant.On("▶ ")
		if focused {
			marker = palette.Primary.On("▶ ")
		}
	}
	line := ansi.Truncate(marker+label, w, "")
	if selected && focused {
		if bg := palette.Highlight.BG(); bg != "" {
			line = bg + ansi.Truncate(padStyled(line, w), w, "") + "\x1b[0m"
		}
	}
	drawLine(scr, r, line)
}

// drawBoxColored is drawBox with explicit border + label colours, so panes
// can repaint their frame for a mode (e.g. the composer's PlanMode tint).
// Each row is painted as its own StyledString; out-of-bounds cells are
// clipped. A degenerate rect (width < 2) is skipped.
func drawBoxColored(scr uv.Screen, r uv.Rectangle, label string, border, labelCol theme.Color) {
	drawFrame(scr, r, frameStyle{Label: label, Border: border, LabelColor: labelCol})
}

// bracketed is the shared token shape used by pane titles, section headings,
// and the quiet top bar. The brackets stay in the border tone while the caller
// owns the inner styling.
func bracketed(inner string) string {
	return palette.Border.On("[") + inner + palette.Border.On("]")
}

// drawPaneHRule paints a standard horizontal divider inside a pane. jointAt is
// an offset from x; pass -1 for a plain rule.
func drawPaneHRule(scr uv.Screen, x, y, w, jointAt int, joint string) {
	drawPaneHRuleColored(scr, x, y, w, jointAt, joint, palette.Border)
}

func drawPaneHRuleColored(scr uv.Screen, x, y, w, jointAt int, joint string, col theme.Color) {
	for i := range w {
		ch := "─"
		if i == jointAt {
			ch = joint
		}
		drawLine(scr, uv.Rect(x+i, y, 1, 1), col.On(ch))
	}
}
