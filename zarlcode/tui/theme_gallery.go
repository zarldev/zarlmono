package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/tui/appearance"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

// themeGallery is the inline, multi-column theme grid that forms the detail of
// the Appearance category. Moving the cursor previews the theme live; enter
// persists it (workspace scope) and keeps it; leaving without committing
// reverts the live preview to whatever was active on entry — so the whole
// theme set is visible and tryable, not a blind enter-to-cycle.
type themeGallery struct {
	ctx       context.Context
	s         *engine.Settings
	workspace string
	store     *appearance.Store
	scope     string
	names     []string
	cursor    int
	onError   func(error)
	origin    theme.Theme
	focused   bool
	cols      int
	effective appearance.Choice
	err       error
}

// themeCellW is one grid cell's width (marker + name + padding).
const themeCellW = 22

// newThemeGalleryWithContext constructs a gallery for a known-valid owner context.
func newThemeGalleryWithContext(ctx context.Context, s *engine.Settings) *themeGallery {
	store, err := appearanceStore()
	g := &themeGallery{ctx: ctx, s: s, names: themeNames(), cols: 3, store: store, scope: "workspace"}
	g.err = err
	g.refresh()
	return g
}

// refresh points the cursor at the persisted (or live) theme.
func (g *themeGallery) refresh() {
	cur := palette.Name
	if g.store != nil {
		if choice, err := g.choice(); err == nil {
			cur = choice.Name
			g.effective = choice
			g.err = nil
		} else {
			g.err = err
			g.report(err)
		}
	}
	for i, n := range g.names {
		if n == cur {
			g.cursor = i
			return
		}
	}
}

// enter captures the current theme for revert-on-cancel.
func (g *themeGallery) enter() {
	g.origin = palette
	g.focused = true
}

// leave reverts the live preview (unless a commit moved origin) and drops focus.
func (g *themeGallery) leave() {
	if g.focused {
		UseTheme(g.origin)
	}
	g.focused = false
}

func (g *themeGallery) selectedName() string {
	if g.cursor < 0 || g.cursor >= len(g.names) {
		return ""
	}
	return g.names[g.cursor]
}

func (g *themeGallery) isPreviewing() bool {
	return g.focused && g.selectedName() != "" && g.selectedName() != g.origin.Name
}

func (g *themeGallery) preview() {
	if g.cursor < 0 || g.cursor >= len(g.names) {
		return
	}
	if t, ok := theme.ByName(g.names[g.cursor]); ok {
		UseTheme(t)
	}
}

// handleKey drives grid navigation with live preview; enter commits. Returns
// true when a commit happened so the host can toast + refresh.
func (g *themeGallery) handleKey(msg tea.KeyPressMsg) bool {
	cols := max(g.cols, 1)
	if msg.String() == "tab" {
		switch g.scope {
		case "workspace":
			g.scope = "global"
		case "global":
			g.scope = "app"
		default:
			if g.workspace != "" {
				g.scope = "workspace"
			} else {
				g.scope = "global"
			}
		}
		return false
	}
	if msg.String() == "r" {
		return g.reset()
	}
	switch msg.String() {
	case "up", "k":
		if g.cursor-cols >= 0 {
			g.cursor -= cols
			g.preview()
		}
	case "down", "j":
		if g.cursor+cols < len(g.names) {
			g.cursor += cols
			g.preview()
		}
	case "left", "h":
		if g.cursor > 0 {
			g.cursor--
			g.preview()
		}
	case "right", "l":
		if g.cursor < len(g.names)-1 {
			g.cursor++
			g.preview()
		}
	case "home", "g":
		g.cursor = 0
		g.preview()
	case "end", "G":
		g.cursor = len(g.names) - 1
		g.preview()
	case "enter", "space", " ":
		if g.cursor >= 0 && g.cursor < len(g.names) {
			return g.commit(g.names[g.cursor])
		}
	}
	return false
}

func (g *themeGallery) commit(name string) bool {
	if g.store == nil {
		g.report(errors.New("appearance store unavailable"))
		return false
	}
	target, err := appearance.TargetFor(g.scope, appearanceApp, g.workspace)
	if err == nil {
		err = g.store.Set(g.ctx, target, name)
	}
	if err != nil {
		g.report(err)
		return false
	}
	if choice, e := g.choice(); e == nil {
		UseTheme(choice.Theme)
		g.effective = choice
		g.origin = palette
	} else {
		g.report(e)
		return false
	}
	return true
}

func (g *themeGallery) reset() bool {
	if g.store == nil {
		g.report(errors.New("appearance store unavailable"))
		return false
	}
	target, err := appearance.TargetFor(g.scope, appearanceApp, g.workspace)
	if err == nil {
		var svc themeSettings
		if g.s != nil && g.s.Svc != nil {
			svc = g.s.Svc
		}
		err = resetThemeScope(g.ctx, g.store, svc, target)
	}
	if err != nil {
		g.report(err)
		return false
	}
	choice, err := g.choice()
	if err != nil {
		g.report(err)
		return false
	}
	UseTheme(choice.Theme)
	g.effective = choice
	g.origin = palette
	return true
}

func (g *themeGallery) report(err error) {
	if g.onError != nil {
		g.onError(err)
	}
}

func (g *themeGallery) choice() (appearance.Choice, error) {
	var svc themeSettings
	if g.s != nil && g.s.Svc != nil {
		svc = g.s.Svc
	}
	return themeChoice(g.ctx, g.store, svc, g.workspace)
}

// detailLines lays the themes into a multi-column grid sized to width, and
// records the column count for navigation. The cursor cell is marked, and the
// grid rows are windowed to `height` so the cursor's row stays visible when the
// grid is taller than the detail region (a narrow terminal yields few columns,
// hence many rows). height <= 0, or a grid that fits, renders from the top.
func (g *themeGallery) detailLines(width, height int) []string {
	cols := max(width/themeCellW, 1)
	g.cols = cols
	name, scope := g.effective.Name, g.effective.Scope
	if name == "" {
		name, scope = g.origin.Name, "unavailable"
	}
	header := terminalChrome().Selection(fmt.Sprintf("effective: %s (%s) · target: %s", name, scope, g.scope), width, false)
	if g.err != nil {
		header = terminalChrome().Selection("notice: "+g.err.Error(), width, false)
	}
	height = max(1, height-1)
	totalRows := (len(g.names) + cols - 1) / cols

	startRow, endRow := 0, totalRows
	if height >= 1 && totalRows > height {
		startRow, endRow = windowAroundCursor(g.cursor/cols, totalRows, height)
	}

	out := []string{header}
	style := terminalChrome()
	for r := startRow; r < endRow; r++ {
		var sb strings.Builder
		for c := range cols {
			i := r*cols + c
			if i >= len(g.names) {
				break
			}
			cell := pad(g.names[i], themeCellW-2)
			sb.WriteString(style.Selection(style.Marker(i == g.cursor, false)+cell, themeCellW, i == g.cursor))
		}
		out = append(out, sb.String())
	}
	return out
}
