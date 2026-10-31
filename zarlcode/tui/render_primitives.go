package tui

import (
	"image/color"
	"strings"

	lg "charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

// lgColor converts zarlcode's theme hex colour into the colour type lipgloss
// expects. Do not pass Color.FG()/BG() here: those return ANSI SGR escapes,
// while lipgloss wants raw hex ("#rrggbb") or an ANSI colour number.
func lgColor(c theme.Color) color.Color {
	if c == "" {
		return lg.NoColor{}
	}
	return lg.Color(string(c))
}

// drawLine paints text into r after clipping to r's display width.
func drawLine(scr uv.Screen, r uv.Rectangle, text string) {
	if r.Dx() < 1 || r.Dy() < 1 {
		return
	}
	uv.NewStyledString(ansi.Truncate(text, r.Dx(), "")).Draw(scr, uv.Rect(r.Min.X, r.Min.Y, r.Dx(), 1))
}

// drawPaddedLine paints text padded/truncated to exactly r's display width.
func drawPaddedLine(scr uv.Screen, r uv.Rectangle, text string) {
	if r.Dx() < 1 || r.Dy() < 1 {
		return
	}
	drawLine(scr, r, padStyled(text, r.Dx()))
}

type frameStyle struct {
	Label      string
	Border     theme.Color
	LabelColor theme.Color
}

// drawFrame paints the standard zarlcode box chrome and returns its drawable
// interior. It is the single primitive for pane, modal, and cockpit borders.
func drawFrame(scr uv.Screen, r uv.Rectangle, style frameStyle) uv.Rectangle {
	w, h := r.Dx(), r.Dy()
	if w < 2 || h < 1 {
		return uv.Rect(r.Min.X, r.Min.Y, 0, 0)
	}
	border := style.Border
	if border == "" {
		border = palette.Border
	}
	labelCol := style.LabelColor
	if labelCol == "" {
		labelCol = palette.Primary
	}
	lineGlyph, vertical := "─", "│"
	tl, tr, bl, br := "┌", "┐", "└", "┘"
	chrome := terminalChrome()
	if chrome.ASCII {
		lineGlyph, vertical = "-", "|"
		tl, tr, bl, br = "+", "+", "+", "+"
	}
	if chrome.Plain {
		border, labelCol = "", ""
	}
	for y := range h {
		var line string
		switch {
		case h == 1:
			line = strings.Repeat(lineGlyph, w)
		case y == 0:
			line = tl + strings.Repeat(lineGlyph, w-2) + tr
		case y == h-1:
			line = bl + strings.Repeat(lineGlyph, w-2) + br
		default:
			line = vertical + strings.Repeat(" ", w-2) + vertical
		}
		drawLine(scr, uv.Rect(r.Min.X, r.Min.Y+y, w, 1), border.On(line))
	}
	if style.Label != "" {
		title := frameTitle(strings.ToLower(strings.TrimSpace(style.Label)), border, labelCol)
		tw := ansi.StringWidth(title)
		if tw <= w-2 {
			x := r.Min.X + 2
			if tw > w-3 {
				x = r.Min.X + 1
			}
			drawLine(scr, uv.Rect(x, r.Min.Y, tw, 1), title)
		}
	}
	if w < 2 || h < 2 {
		return uv.Rect(r.Min.X, r.Min.Y, 0, 0)
	}
	return uv.Rect(r.Min.X+1, r.Min.Y+1, w-2, h-2)
}

func frameTitle(label string, border, labelCol theme.Color) string {
	return border.On("[") + labelCol.On(label) + border.On("]")
}
