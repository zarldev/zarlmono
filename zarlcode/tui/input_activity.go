package tui

import (
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// drawInputActivity overlays the existing separator, leaving all input rows and
// the independent error/status row untouched. Existing UI ticks own updates.
func (m *UI) drawInputActivity(scr uv.Screen, area uv.Rectangle) {
	run := &m.session.Run
	if !run.Running || area.Dx() < 10 || area.Dy() < 1 {
		return
	}
	elapsed := time.Duration(0)
	if !run.turnStartedAt.IsZero() {
		elapsed = max(0, time.Since(run.turnStartedAt))
	}
	seconds := int(elapsed / time.Second)
	timer := itoa(seconds/60) + ":"
	if seconds%60 < 10 {
		timer += "0"
	}
	timer += itoa(seconds % 60)
	width := area.Dx() - 4
	label := runActivityGlyph(m.frame, true) + " " + run.activityLabel()
	if tps := run.liveTokPerSec(); tps > 0 && width >= 70 {
		label += "  ·  " + itoa(int(tps+0.5)) + " tok/s"
	}
	labelWidth := width - ansi.StringWidth(timer) - 3
	text := palette.Muted.On(timer)
	if labelWidth > 2 {
		text = palette.Success.On(ansi.Truncate(label, labelWidth, "…")) + "  " + text
	}
	text = " " + text + " "
	drawLine(scr, uv.Rect(area.Min.X+1, area.Min.Y, ansi.StringWidth(text), 1), text)
}
