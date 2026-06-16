package tui

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// drawSidebar paints the cockpit into r. When the pane interior is wide and
// tall enough it draws the rich graphical cockpit (gauge + context graph +
// sparklines + cost + tools); otherwise it degrades to the flat row list.
func (m *UI) drawSidebar(scr uv.Screen, r uv.Rectangle) {
	drawFrame(scr, r, frameStyle{Border: palette.Border})

	// The title bar shows the pane name plus live run state:
	//   ┌─[state] [⠄ idle]──────┐   ┌─[state] [⠋ running]──────────────┐
	m.drawRunTitleStatus(scr, r, true)

	w, h := r.Dx(), r.Dy()
	if w < 6 || h < 3 {
		return
	}
	// sidePad is a one-column gutter inside the border on each side, so
	// content doesn't butt against the frame. Section rules override this
	// below so they read as dividers in the frame itself.
	const sidePad = 1
	innerW := w - 2 - 2*sidePad
	innerH := h - 2

	lines := m.sidebarLines(innerW, innerH)

	for i, ln := range lines {
		if i >= innerH {
			break
		}
		// Section rules are emitted with ANSI styling, so detect them after
		// stripping escapes. They span the full pane width to avoid the normal
		// one-column content gutters rendering as "│ ├─... │".
		if strings.HasPrefix(ansi.Strip(ln), "├") {
			drawLine(scr, uv.Rect(r.Min.X, r.Min.Y+1+i, w, 1), sidebarSectionRule(ln, w))
		} else {
			drawLine(scr, uv.Rect(r.Min.X+1+sidePad, r.Min.Y+1+i, innerW, 1), ln)
		}
	}
}

func sidebarSectionRule(line string, width int) string {
	if width < 1 {
		return ""
	}
	lineW := ansi.StringWidth(line)
	if lineW >= width {
		return ansi.Truncate(line, width, "")
	}
	fill := width - lineW - 1
	if fill < 0 {
		fill = 0
	}
	return line + palette.Border.On(strings.Repeat("─", fill)+"┤")
}

func (m *UI) sidebarLines(innerW, innerH int) []string {
	if innerW >= cockpitMinWidth && innerH >= cockpitMinHeight {
		return m.cockpitLines(innerW)
	}
	return m.session.Run.lines()
}

// drawRunTitleStatus paints the run pane title plus state — "[state] [⠄ idle]"
// in the sidebar, or "[ƶarl/code] [chat] [model] [⠄ idle]" in the timeline
// pane. Timeline titles carry the global app/mode/model context now that there
// is no standalone top header line.
func (m *UI) drawRunTitleStatus(scr uv.Screen, r uv.Rectangle, showLabel bool) {
	title := m.runTitleStatus(showLabel)

	// Title sits right after "┌─" (cols 0,1) at col 2.
	x := r.Min.X + 2
	// Width = exactly the title text so we overwrite only those cells and
	// leave drawBox's border dashes intact on both sides (a wider rect would
	// blank the closing dashes up to ┐). Clip to fit before the right border.
	w := ansi.StringWidth(title)
	if avail := r.Max.X - 1 - x; w > avail {
		w = avail
	}
	if w < 1 {
		return
	}
	drawLine(scr, uv.Rect(x, r.Min.Y, w, 1), title)
}

func (m *UI) runTitleStatus(showLabel bool) string {
	s := &m.session.Run
	glyph, tone := runActivityGlyph(m.frame, false), palette.Muted
	word := "idle"
	if s.Running {
		glyph = runActivityGlyph(m.frame, true)
		tone, word = palette.Success, "running"
	}
	var title string
	if showLabel {
		title = bracketed(palette.Primary.On("state")) + " " + bracketed(tone.On(glyph+" "+word))
	} else {
		parts := []string{
			bracketed(palette.Primary.On(appDisplayName)),
			m.headerModeBadge(),
		}
		model := m.session.Model
		if model == "" && m.session != nil {
			model = m.session.Model
		}
		if model != "" {
			parts = append(parts, bracketed(palette.Subtle.On(strings.ToLower(model))))
		}
		parts = append(parts, bracketed(tone.On(glyph+" "+word)))
		title = strings.Join(parts, " ")
	}
	if !showLabel && s.Running {
		if tps := s.liveTokPerSec(); tps > 0 {
			title += " " + bracketed(palette.Info.On(itoa(int(tps+0.5))+" tok/s"))
		}
	}
	return title
}
