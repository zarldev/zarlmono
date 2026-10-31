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
	bar := sidebarHeading(title) + "  " + tabCluster(terminalChrome(), tabs, active)
	if ansi.StringWidth(bar) > width && active >= 0 && active < len(tabs) {
		bar = sidebarHeading(title) + "  " + tabCluster(terminalChrome(), []string{tabs[active]}, 0)
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
