package tui

import (
	"image/color"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

// transcriptPaneGeometry owns the forward and inverse coordinate transform.
// The first inset cell is reserved for browsing; neither selection nor role
// styling changes the text lane. The final pane column belongs to the scrollbar.
type transcriptPaneGeometry struct {
	surface uv.Rectangle
	content uv.Rectangle
}

func transcriptGeometry(pane uv.Rectangle) transcriptPaneGeometry {
	surface := uv.Rect(pane.Min.X, pane.Min.Y+2, max(0, pane.Dx()-scrollbarWidth), max(0, pane.Dy()-2))
	content := uv.Rect(surface.Min.X+2, surface.Min.Y, transcriptContentWidth(surface.Dx()-3), surface.Dy())
	return transcriptPaneGeometry{surface: surface, content: content}
}

func transcriptBackground(it item, user, assistant, queued color.Color) color.Color {
	switch it := it.(type) {
	case *queuedUserItem:
		if !it.injected {
			return queued
		}
		return user
	case *userItem:
		return user
	case *assistantItem, *thinkingItem, *groupItem, *planItem, *subAgentItem, *skillsItem, *toolItem, *diffItem:
		return assistant
	default:
		return surfaceColor(palette.Bg)
	}
}

func surfaceColor(c theme.Color) color.Color {
	if c == "" {
		return nil
	}
	return lgColor(c)
}

// drawRows composes defaults on cells, not ANSI strings. It touches only visible
// rows and never pads item caches. Explicit code/image backgrounds survive on
// unselected rows; selected rows intentionally use the family's selection colors.
func (tl *timeline) drawRows(scr uv.Screen, g transcriptPaneGeometry, lines []string, keepEmoji bool) {
	userBg, assistantBg := palette.ConversationBackgrounds()
	user, assistant := surfaceColor(userBg), surfaceColor(assistantBg)
	queued := surfaceColor(queuedUserBackground)
	neutral := surfaceColor(palette.Bg)
	style := terminalChrome()
	selectionFg, selectionBg := style.SelectionColors()
	selectedFg, selectedBg := surfaceColor(selectionFg), surfaceColor(selectionBg)
	lo, hi := tl.selectionRange()
	if tl.selectionActive() {
		// The trailing content buffer is padding, not selectable message text.
		hi = min(hi, tl.totalLines(g.content.Dx())-2)
	}
	surface := transcriptSurfaceScreen{Screen: scr}
	for row, line := range lines {
		if !keepEmoji {
			line = stripWide(line)
		}
		y := g.content.Min.Y + row
		bg := neutral
		index := tl.visItem[row]
		if index >= 0 {
			bg = transcriptBackground(tl.items[index], user, assistant, queued)
		}
		cursor := tl.browsing && !tl.selectionActive() && index == tl.sel && tl.visLocal[row] == tl.selLocal
		selected := cursor || (tl.selectionActive() && tl.scrollTop+row >= lo && tl.scrollTop+row <= hi)
		surface.foreground = nil
		if selected {
			surface.foreground = selectedFg
			if selectedBg != nil {
				bg = selectedBg
			}
		}
		surface.background, surface.selected = bg, selected && selectedBg != nil
		// Resolve resets as cells are written, without a second content pass.
		x := g.content.Min.X
		if index >= 0 && tl.visLocal[row] == 0 {
			switch tl.items[index].(type) {
			case *userItem, *queuedUserItem, *assistantItem:
				x-- // Role headings sit one cell left of their body, outside the browse marker.
			}
		}
		drawLine(&surface, uv.Rect(x, y, g.content.Max.X-x, 1), line)
		if cursor {
			drawLine(&surface, uv.Rect(g.surface.Min.X, y, 1, 1), style.PaneLabel(style.Marker(true, false), true))
		}
		paintTranscriptBackground(scr, uv.Rect(g.surface.Min.X, y, g.content.Min.X-g.surface.Min.X, 1), bg, selected && selectedBg != nil)
		paintTranscriptBackground(scr, uv.Rect(g.content.Max.X, y, g.surface.Max.X-g.content.Max.X, 1), bg, selected && selectedBg != nil)
	}
}

// transcriptSurfaceScreen supplies row-local defaults without modifying ANSI
// streams. One scratch value is reused; the screen owns copies of written cells.
type transcriptSurfaceScreen struct {
	uv.Screen
	background color.Color
	foreground color.Color
	selected   bool
	cell       uv.Cell
}

func (s *transcriptSurfaceScreen) SetCell(x, y int, cell *uv.Cell) {
	if s.background == nil && s.foreground == nil {
		s.Screen.SetCell(x, y, cell)
		return
	}
	if cell == nil {
		s.cell = uv.EmptyCell
	} else {
		s.cell = *cell
	}
	if s.background != nil && (s.selected || s.cell.Style.Bg == nil) {
		s.cell.Style.Bg = s.background
	}
	if s.foreground != nil {
		s.cell.Style.Fg = s.foreground
	}
	s.Screen.SetCell(x, y, &s.cell)
}

// Reuse one value while copying cells back to their screen owner. Skipping wide
// continuation cells avoids splitting a grapheme or clobbering its leading cell.
func paintTranscriptBackground(scr uv.Screen, row uv.Rectangle, bg color.Color, selected bool) {
	if bg == nil {
		return
	}
	var painted uv.Cell
	for x := row.Min.X; x < row.Max.X; {
		cell := scr.CellAt(x, row.Min.Y)
		if cell == nil {
			break
		}
		width := max(1, cell.Width)
		if cell.Width != 0 && (selected || cell.Style.Bg == nil) {
			painted = *cell
			painted.Style.Bg = bg
			scr.SetCell(x, row.Min.Y, &painted)
		}
		x += width
	}
}
