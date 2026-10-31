package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zkit/tui/appearance"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

const themePickerVisible = 18
const themePickerMinWidth = 40

type themePicker struct {
	names     []string
	cursor    int
	origin    theme.Theme
	scoped    *appearance.Picker
	store     *appearance.Store
	svc       themeSettings
	ctx       context.Context
	workspace string
	err       error
	effective appearance.Choice
}

// newScopedThemePicker provides the scoped, persistent picker for a running session.
func newScopedThemePicker(ctx context.Context, svc themeSettings, workspace string) *themePicker {
	p := buildThemePicker()
	p.ctx, p.svc, p.workspace = ctx, svc, workspace
	store, err := appearanceStore()
	if err == nil {
		p.store = store
		p.scoped, err = appearance.NewPicker(ctx, store, appearanceApp, workspace, palette)
	}
	if err == nil {
		p.effective, err = themeChoice(ctx, store, svc, workspace)
	}
	p.err = err
	return p
}

// newThemePicker provides an unscoped preview-only picker.
func newThemePicker() *themePicker { return buildThemePicker() }

func buildThemePicker() *themePicker {
	bs := theme.Builtins()
	names := make([]string, 0, len(bs))
	for _, t := range bs {
		names = append(names, t.Name)
	}
	slices.Sort(names)

	p := &themePicker{names: names, origin: palette}
	for i, n := range names {
		if n == palette.Name {
			p.cursor = i
		}
	}
	return p
}

func (p *themePicker) preview() {
	if p.scoped != nil {
		UseTheme(p.scoped.Preview())
		return
	}
	if p.cursor < 0 || p.cursor >= len(p.names) {
		return
	}
	if t, ok := theme.ByName(p.names[p.cursor]); ok {
		UseTheme(t)
	}
}

func (p *themePicker) handleKey(msg tea.KeyPressMsg) action {
	if p.scoped != nil {
		switch msg.String() {
		case "up", "k":
			p.scoped.Move(-1)
			p.preview()
		case "down", "j":
			p.scoped.Move(1)
			p.preview()
		case "tab":
			p.scoped.CycleScope()
		case "enter":
			_, err := p.scoped.Commit(p.ctx)
			if err == nil {
				choice, e := themeChoice(p.ctx, p.store, p.svc, p.workspace)
				err = e
				if e == nil {
					UseTheme(choice.Theme)
				}
			}
			if err == nil {
				return actionClose{}
			}
			p.err = err
		case "r":
			target, err := appearance.TargetFor(p.scoped.Scope(), appearanceApp, p.workspace)
			if err == nil {
				err = resetThemeScope(p.ctx, p.store, p.svc, target)
			}
			if err == nil {
				choice, e := themeChoice(p.ctx, p.store, p.svc, p.workspace)
				err = e
				if e == nil {
					UseTheme(choice.Theme)
				}
			}
			if err == nil {
				return actionClose{}
			}
			p.err = err
		case "esc", "ctrl+t", "q":
			UseTheme(p.origin)
			return actionClose{}
		}
		return actionNone{}
	}
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
			return actionSetTheme{name: name}
		}
	case "esc", "ctrl+t", "q":
		UseTheme(p.origin)
		return actionClose{}
	}
	return actionNone{}
}

func (p *themePicker) draw(scr uv.Screen, area uv.Rectangle) {
	w, h := area.Dx(), area.Dy()
	if h < 8 {
		return
	}
	hint := keyLegend(keyHint{"↑↓", "navigate"}, keyHint{"enter", "select"}, keyHint{"esc", "close"})
	if w < 40 {
		hint = keyLegend(keyHint{"esc", "close"}, keyHint{"↑↓", "move"}, keyHint{"enter", "use"})
	}
	boxW := themePickerMinWidth
	if contentW := p.longestNameWidth() + 6; contentW > boxW {
		boxW = contentW
	}
	if footerW := ansi.StringWidth(hint) + 4; footerW > boxW {
		boxW = footerW
	}
	if p.scoped != nil {
		// Scoped source/target metadata and save/inherit controls need more
		// room than the former name-only quick picker.
		boxW = max(boxW, min(80, w-4))
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

	if p.scoped != nil {
		lines := p.scoped.LinesWithStyle(innerW, lay.Body.Dy(), terminalChrome())
		if len(lines) > 1 && p.effective.Name != "" {
			label := fmt.Sprintf("effective: %s (%s)", p.effective.Name, p.effective.Scope)
			lines[1] = terminalChrome().Selection(label, innerW, false)
		}
		for i, line := range lines {
			drawPaddedLine(scr, uv.Rect(innerX, lay.Body.Min.Y+i, innerW, 1), line)
		}
		if p.err != nil {
			drawPaddedLine(scr, uv.Rect(innerX, footerY, innerW, 1), palette.Error.On(ansi.Truncate(p.err.Error(), innerW, "")))
		}
		return
	}
	drawPaddedLine(scr, uv.Rect(innerX, lay.Context.Min.Y, innerW, 1), overlayTopBar("themes", nil, 0, fmt.Sprintf("%d/%d", p.cursor+1, len(p.names)), innerW))
	drawPaddedLine(scr, uv.Rect(innerX, lay.Body.Min.Y, innerW, 1), palette.Border.On(strings.Repeat("─", innerW)))

	start, end, up, down := listWindow(p.cursor, len(p.names), visibleRows)
	y := listY
	if up {
		drawPaddedLine(scr, uv.Rect(innerX, y, innerW, 1), palette.Muted.On("  ↑ more"))
		y++
	}
	style := terminalChrome()
	for i := start; i < end; i++ {
		line := listRow(style, p.names[i], "", innerW, i == p.cursor)
		drawPaddedLine(scr, uv.Rect(innerX, y, innerW, 1), line)
		y++
	}
	if down {
		drawPaddedLine(scr, uv.Rect(innerX, y, innerW, 1), palette.Muted.On("  ↓ more"))
	}

	drawPaddedLine(scr, uv.Rect(innerX, footerY, innerW, 1), hint)
}

func (m *UI) openThemePicker() {
	if m.settings == nil {
		m.overlay.push(newThemePicker())
		return
	}
	m.overlay.push(newScopedThemePicker(m.appContext(), m.settings.Svc, m.session.WorkspaceDir))
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
