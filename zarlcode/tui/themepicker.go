package tui

import (
	"strings"
	"fmt"
	"slices"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

const themePickerVisible = 18
const themePickerMinWidth = 40

// themePicker is a modal list of the builtin themes. It previews each theme
// live as the cursor moves (so you see it before committing, instead of
// mashing enter to cycle blindly) and reverts to the theme that was active
// when it opened if you cancel. enter commits: in quick mode (ctrl+t) via
// actionSetTheme; with onPick set (the settings appearance row) via the
// callback, which persists the choice.
type themePicker struct {
	names  []string
	cursor int
	origin string       // theme active when opened, for revert-on-cancel
	onPick func(string) // nil => quick mode (enter → actionSetTheme, no persist)
}

// newThemePicker is the ctrl+t quick switcher: live preview, no persistence.
func newThemePicker() *themePicker { return buildThemePicker(nil) }

// newThemePickerFor backs the settings appearance row: enter calls onPick so
// the caller persists the choice; esc reverts the live preview.
func newThemePickerFor(onPick func(string)) *themePicker { return buildThemePicker(onPick) }

func buildThemePicker(onPick func(string)) *themePicker {
	bs := theme.Builtins()
	names := make([]string, 0, len(bs))
	for _, t := range bs {
		names = append(names, t.Name)
	}
	slices.Sort(names)

	p := &themePicker{names: names, origin: palette.Name, onPick: onPick}
	for i, n := range names {
		if n == palette.Name {
			p.cursor = i
		}
	}
	return p
}

// preview applies the theme under the cursor live, so the whole UI repaints
// in it while the picker is open.
func (p *themePicker) preview() {
	if p.cursor < 0 || p.cursor >= len(p.names) {
		return
	}
	if t, ok := theme.ByName(p.names[p.cursor]); ok {
		UseTheme(t)
	}
}

func (p *themePicker) handleKey(msg tea.KeyPressMsg) action {
	switch msg.String() {
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
			p.preview()
		}
	case "down", "j":
		if p.cursor < len(p.names)-1 {
			p.cursor++
			p.preview()
		}
	case "enter":
		if len(p.names) > 0 {
			name := p.names[p.cursor]
			if p.onPick != nil {
				p.onPick(name)
				return actionClose{}
			}
			return actionSetTheme{name: name}
		}
	case "esc", "ctrl+t", "q":
		if t, ok := theme.ByName(p.origin); ok {
			UseTheme(t) // undo the live preview
		}
		return actionClose{}
	}
	return actionNone{}
}

func (p *themePicker) draw(scr uv.Screen, area uv.Rectangle) {
	w, h := area.Dx(), area.Dy()
	if w < 30 || h < 8 {
		return
	}
	hint := keyLegend(keyHint{"↑↓", "navigate"}, keyHint{"enter", "select"}, keyHint{"esc", "close"})
	boxW := themePickerMinWidth
	if contentW := p.longestNameWidth() + 6; contentW > boxW {
		boxW = contentW
	}
	if footerW := ansi.StringWidth(hint) + 4; footerW > boxW {
		boxW = footerW
	}
	if maxW := w - 4; boxW > maxW {
		boxW = maxW
	}
	boxH := min(themePickerVisible+5, h-2)
	lay, ok := drawDialogPane(scr, area, "themes", boxW, boxH, palette.Border, palette.Primary)
	if !ok {
		return
	}
	innerW, innerX := lay.Body.Dx(), lay.Body.Min.X
	listY := lay.Body.Min.Y + 1
	footerY := lay.Footer.Min.Y
	visibleRows := max(1, lay.Body.Dy()-1)

	drawPaddedLine(scr, uv.Rect(innerX, lay.Context.Min.Y, innerW, 1), overlayTopBar("themes", nil, 0, fmt.Sprintf("%d/%d", p.cursor+1, len(p.names)), innerW))
	drawPaddedLine(scr, uv.Rect(innerX, lay.Body.Min.Y, innerW, 1), palette.Border.On(strings.Repeat("─", innerW)))

	start, end, up, down := listWindow(p.cursor, len(p.names), visibleRows)
	y := listY
	if up {
		drawPaddedLine(scr, uv.Rect(innerX, y, innerW, 1), palette.Muted.On("  ↑ more"))
		y++
	}
	for i := start; i < end; i++ {
		var line string
		if i == p.cursor {
			line = palette.Primary.On("▸ " + p.names[i])
		} else {
			line = "  " + palette.Subtle.On(p.names[i])
		}
		drawPaddedLine(scr, uv.Rect(innerX, y, innerW, 1), line)
		y++
	}
	if down {
		drawPaddedLine(scr, uv.Rect(innerX, y, innerW, 1), palette.Muted.On("  ↓ more"))
	}

	drawPaddedLine(scr, uv.Rect(innerX, footerY, innerW, 1), hint)
}

func (p *themePicker) longestNameWidth() int {
	w := 0
	for _, name := range p.names {
		if n := ansi.StringWidth(name); n > w {
			w = n
		}
	}
	return w
}
