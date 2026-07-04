package tui

import (
	"context"
	"log/slog"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/prefs"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

// themeGallery is the inline, multi-column theme grid that forms the detail of
// the Appearance category. Moving the cursor previews the theme live; enter
// persists it (workspace scope) and keeps it; leaving without committing
// reverts the live preview to whatever was active on entry — so the whole
// theme set is visible and tryable, not a blind enter-to-cycle.
type themeGallery struct {
	ctx     context.Context
	s       *engine.Settings
	names   []string
	cursor  int
	origin  string // theme active when focus entered, for revert-on-leave
	focused bool
	cols    int // columns last rendered, drives grid navigation
}

// themeCellW is one grid cell's width (marker + name + padding).
const themeCellW = 22

func newThemeGalleryWithContext(ctx context.Context, s *engine.Settings) *themeGallery {
	if ctx == nil {
		ctx = context.Background()
	}
	g := &themeGallery{ctx: ctx, s: s, names: themeNames(), cols: 3}
	g.refresh()
	return g
}

// refresh points the cursor at the persisted (or live) theme.
func (g *themeGallery) refresh() {
	cur := palette.Name
	if g.s != nil && g.s.Svc != nil {
		if v, ok, err := g.s.Svc.GetSetting(g.ctx, prefs.ScopeEffective, prefs.KeyTheme); err == nil && ok && v.Value != "" {
			cur = v.Value
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
	g.origin = palette.Name
	g.focused = true
}

// leave reverts the live preview (unless a commit moved origin) and drops focus.
func (g *themeGallery) leave() {
	if g.focused {
		if t, ok := theme.ByName(g.origin); ok {
			UseTheme(t)
		}
	}
	g.focused = false
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
			g.commit(g.names[g.cursor])
			return true
		}
	}
	return false
}

func (g *themeGallery) commit(name string) {
	if g.s != nil && g.s.Svc != nil {
		if err := g.s.Svc.SetSetting(g.ctx, prefs.ScopeWorkspace, prefs.KeyTheme, name); err != nil {
			slog.WarnContext(g.ctx, "persist theme selection", "err", err, "theme", name)
		}
	}
	if t, ok := theme.ByName(name); ok {
		UseTheme(t)
	}
	g.origin = name // committed — keep it when leaving
}

// detailLines lays the themes into a multi-column grid sized to width, and
// records the column count for navigation. The cursor cell is marked, and the
// grid rows are windowed to `height` so the cursor's row stays visible when the
// grid is taller than the detail region (a narrow terminal yields few columns,
// hence many rows). height <= 0, or a grid that fits, renders from the top.
func (g *themeGallery) detailLines(width, height int) []string {
	cols := max(width/themeCellW, 1)
	g.cols = cols
	totalRows := (len(g.names) + cols - 1) / cols

	startRow, endRow := 0, totalRows
	if height >= 1 && totalRows > height {
		startRow, endRow = windowAroundCursor(g.cursor/cols, totalRows, height)
	}

	out := make([]string, 0, endRow-startRow)
	for r := startRow; r < endRow; r++ {
		var sb strings.Builder
		for c := range cols {
			i := r*cols + c
			if i >= len(g.names) {
				break
			}
			cell := pad(g.names[i], themeCellW-2)
			if i == g.cursor {
				sb.WriteString(palette.Primary.On("▸ " + cell))
			} else {
				sb.WriteString(palette.Subtle.On("  " + cell))
			}
		}
		out = append(out, sb.String())
	}
	return out
}
