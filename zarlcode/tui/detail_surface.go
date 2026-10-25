package tui

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// detailViewLayout keeps the dashboard and plan readers on the main screen's
// two-cell inset, with one title rule and a footer outside scrollable content.
type detailViewLayout struct {
	Header uv.Rectangle
	Body   uv.Rectangle
	Footer uv.Rectangle
}

func detailViewGeometry(area uv.Rectangle) detailViewLayout {
	x, width := area.Min.X+2, max(0, area.Dx()-4)
	return detailViewLayout{
		Header: uv.Rect(x, area.Min.Y, width, 1),
		Body:   uv.Rect(x, area.Min.Y+2, width, max(0, area.Dy()-3)),
		Footer: uv.Rect(x, area.Max.Y-1, width, 1),
	}
}

func drawDetailView(scr uv.Screen, area uv.Rectangle, title string, tabs []string, active int) detailViewLayout {
	l := detailViewGeometry(area)
	if l.Body.Empty() {
		return l
	}
	drawLine(scr, l.Header, detailViewTabs(title, tabs, active, l.Header.Dx()))
	drawLine(scr, uv.Rect(area.Min.X, area.Min.Y+1, area.Dx(), 1), palette.Border.On(strings.Repeat("─", area.Dx())))
	return l
}

func detailViewTabs(title string, tabs []string, active, width int) string {
	parts := []string{sidebarHeading(title)}
	for i, tab := range tabs {
		if i == active {
			parts = append(parts, palette.Primary.On("\x1b[1m"+tab+"\x1b[22m"))
		} else {
			parts = append(parts, palette.Subtle.On(tab))
		}
	}
	bar := strings.Join(parts, "  ")
	if ansi.StringWidth(bar) > width {
		bar = sidebarHeading(title) + "  " + palette.Primary.On("\x1b[1m"+tabs[active]+"\x1b[22m")
	}
	return ansi.Truncate(bar, width, "…")
}

// Keep navigation hints readable before admitting optional position metadata.
func drawDetailFooter(scr uv.Screen, r uv.Rectangle, hints, status string) {
	if ansi.StringWidth(hints)+ansi.StringWidth(status)+2 > r.Dx() {
		status = ""
	}
	drawPaneRow(scr, r, palette.Subtle.On(hints), palette.Subtle.On(status))
}
