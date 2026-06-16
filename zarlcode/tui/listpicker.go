package tui

import (
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
	lines := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		if i == p.cursor {
			lines = append(lines, palette.Primary.On("▸ "+p.items[i]))
		} else {
			lines = append(lines, "  "+palette.Subtle.On(p.items[i]))
		}
	}
	drawDialogBox(scr, area, p.title+"  (↑↓ enter)", lines)
}
