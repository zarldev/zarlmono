package tui

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/version"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

// drawSplash is the common full-screen startup composition used by the intro
// screen and pre-launch vault unlock. The logo is centered line-by-line, while
// the caller's info block is centered as one aligned column below it.
func drawSplash(scr uv.Screen, area uv.Rectangle, logoCol theme.Color, infoBlock []string) {
	w, h := area.Dx(), area.Dy()
	if w <= 0 || h <= 0 {
		return
	}

	logo := strings.Trim(introLogoLarge, "\n")
	logoBlock := make([]string, 0, len(strings.Split(logo, "\n"))+1)
	for _, l := range strings.Split(logo, "\n") {
		logoBlock = append(logoBlock, logoCol.On(l))
	}
	logoBlock = append(logoBlock, palette.Muted.On(appDisplayName+" "+version.String()))

	infoW := 0
	for _, l := range infoBlock {
		if lw := ansi.StringWidth(l); lw > infoW {
			infoW = lw
		}
	}
	if infoW > w {
		infoW = w
	}
	infoH := len(infoBlock)
	logoH := len(logoBlock)
	totalH := logoH + infoH

	startY := area.Min.Y + (h-totalH)/2
	if startY < area.Min.Y {
		startY = area.Min.Y
	}

	y := startY
	for _, line := range logoBlock {
		lw := ansi.StringWidth(line)
		x := area.Min.X + (w-lw)/2
		if x < area.Min.X {
			x = area.Min.X
		}
		drawLine(scr, uv.Rect(x, y, lw, 1), line)
		y++
	}

	infoX := area.Min.X + (w-infoW)/2
	if infoX < area.Min.X {
		infoX = area.Min.X
	}
	for i, line := range infoBlock {
		if y+i >= area.Max.Y {
			break
		}
		pw := infoW - ansi.StringWidth(line)
		if pw > 0 {
			line += strings.Repeat(" ", pw)
		}
		drawPaddedLine(scr, uv.Rect(infoX, y+i, infoW, 1), line)
	}
}

// splashPromptBoxLines returns the shared prompt-box chrome used by startup
// splash screens. body is called with the usable text width inside the box and
// should return one or more already-styled display lines.
func splashPromptBoxLines(width int, border, accent theme.Color, body func(textWidth int) []string) []string {
	boxW := width - 8
	if boxW > 96 {
		boxW = 96
	}
	if boxW < 34 {
		boxW = 34
	}
	inner := boxW - 2
	textWidth := inner - 2
	if textWidth < 1 {
		textWidth = 1
	}
	display := body(textWidth)
	if len(display) == 0 {
		display = []string{""}
	}

	out := []string{border.On("┌" + strings.Repeat("─", boxW-2) + "┐")}
	for i, line := range display {
		prefix := "  "
		if i == 0 {
			prefix = "› "
		}
		out = append(out, border.On("│")+padStyled(accent.On(prefix)+line, inner)+border.On("│"))
	}
	out = append(out, border.On("└"+strings.Repeat("─", boxW-2)+"┘"))
	return out
}
