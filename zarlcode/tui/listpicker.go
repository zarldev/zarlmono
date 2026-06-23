package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

const listPickerVisible = 14

// listPicker is a generic modal single-choice list. onPick is invoked with
// the chosen item just before the picker closes; the caller's closure
// performs the effect (e.g. commit a setting, or drop to custom entry).
// Used for model selection from a provider's fetched list.
type listPicker struct {
	title  string
	items  []string
	cursor int
	onPick func(string)
}

func newListPicker(title string, items []string, current string, onPick func(string)) *listPicker {
	p := &listPicker{title: title, items: items, onPick: onPick}
	for i, it := range items {
		if it == current {
			p.cursor = i
		}
	}
	return p
}

func (p *listPicker) handleKey(msg tea.KeyPressMsg) action {
	switch msg.String() {
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		if p.cursor < len(p.items)-1 {
			p.cursor++
		}
	case "enter", "space", " ":
		if p.cursor >= 0 && p.cursor < len(p.items) && p.onPick != nil {
			p.onPick(p.items[p.cursor])
		}
		return actionClose{}
	case "esc", "q":
		return actionClose{}
	}
	return actionNone{}
}

func (p *listPicker) draw(scr uv.Screen, area uv.Rectangle) {
	start := 0
	if p.cursor >= listPickerVisible {
		start = p.cursor - listPickerVisible + 1
	}
	end := start + listPickerVisible
	if end > len(p.items) {
		end = len(p.items)
	}
	boxW := min(72, area.Dx()-4)
	boxH := min(listPickerVisible+5, area.Dy()-2)
	lay, ok := drawDialogPane(scr, area, p.title, boxW, boxH, palette.Border, palette.Primary)
	if !ok {
		return
	}
	innerW, innerX := lay.Body.Dx(), lay.Body.Min.X
	drawPaddedLine(scr, uv.Rect(innerX, lay.Context.Min.Y, innerW, 1), overlayTopBar(p.title, nil, 0, fmt.Sprintf("%d choices", len(p.items)), innerW))
	drawPaddedLine(scr, uv.Rect(innerX, lay.Body.Min.Y, innerW, 1), palette.Border.On(strings.Repeat("─", innerW)))
	y := lay.Body.Min.Y + 1
	for i := start; i < end && y < lay.Footer.Min.Y; i++ {
		if i == p.cursor {
			drawPaddedLine(scr, uv.Rect(innerX, y, innerW, 1), palette.Primary.On("▸ "+p.items[i]))
		} else {
			drawPaddedLine(scr, uv.Rect(innerX, y, innerW, 1), "  "+palette.Subtle.On(p.items[i]))
		}
		y++
	}
	hint := keyLegend(keyHint{"↑↓", "navigate"}, keyHint{"enter", "select"}, keyHint{"esc", "close"})
	drawPaddedLine(scr, uv.Rect(innerX, lay.Footer.Min.Y, innerW, 1), hint)
}
