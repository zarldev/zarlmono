package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

type startupFailurePane struct {
	wsRoot string
	title  string
	err    string
}

func newStartupFailurePane(wsRoot, title, err string) *startupFailurePane {
	return &startupFailurePane{wsRoot: wsRoot, title: title, err: strings.TrimSpace(err)}
}

func (p *startupFailurePane) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "enter", "esc", "q", "ctrl+c":
		return tea.Quit
	}
	return nil
}

func (p *startupFailurePane) draw(scr uv.Screen, area uv.Rectangle) {
	if p == nil {
		return
	}
	info := []string{
		palette.Error.On("startup failed"),
	}
	if p.title != "" {
		info = append(info, palette.Muted.On(p.title))
	}
	info = append(info, "")
	info = append(info, p.errorLines(area.Dx())...)
	if p.wsRoot != "" {
		info = append(info, "", palette.Muted.On("workspace  ")+palette.Subtle.On(p.wsRoot))
	}
	info = append(info, "", p.footer())
	drawSplash(scr, area, palette.Error, info)
}

func (p *startupFailurePane) errorLines(width int) []string {
	if width <= 0 {
		width = 80
	}
	return splashPromptBoxLines(width, palette.Error, palette.Error, func(textWidth int) []string {
		if textWidth < 1 {
			textWidth = 1
		}
		return wrapText(p.err, textWidth)
	})
}

func (p *startupFailurePane) footer() string {
	key := func(k string) string { return palette.Subtle.On(k) }
	mut := func(s string) string { return palette.Muted.On(s) }
	parts := []string{
		fmt.Sprintf("%s%s", key("ctrl+s"), mut(" settings")),
		fmt.Sprintf("%s%s", key("enter"), mut(" quit")),
		fmt.Sprintf("%s%s", key("esc"), mut(" quit")),
	}
	return strings.Join(parts, mut("    "))
}
