package chrome

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

// Logo frames an application's original wordmark with one blank row above and
// below and one thin divider. All rows occupy width cells. It returns no rows
// unless the complete artwork and its framing fit; it never crops or rewrites art.
func (s Style) Logo(art string, width, maxRows int) []string {
	if width < 1 || maxRows < 1 || art == "" {
		return nil
	}
	lines := strings.Split(strings.Trim(art, "\n"), "\n")
	if len(lines)+3 > maxRows {
		return nil
	}
	artWidth := 0
	for _, line := range lines {
		artWidth = max(artWidth, ansi.StringWidth(line))
	}
	if artWidth == 0 || artWidth+2 > width {
		return nil
	}
	padding := strings.Repeat(" ", (width-artWidth)/2)
	rows := make([]string, 0, len(lines)+3)
	rows = append(rows, strings.Repeat(" ", width))
	for _, line := range lines {
		rows = append(rows, fit(padding+s.paint(s.Theme.Primary, line), width))
	}
	rows = append(rows, strings.Repeat(" ", width), s.Rule("", width, false))
	return rows
}

// PaneLabel returns an unpadded heading for composition with inline detail.
// It preserves caller text and uses the same semantic focus roles as PaneTitle.
func (s Style) PaneLabel(title string, focused bool) string {
	color := s.Theme.Subtle
	if focused {
		color = s.Theme.Primary
	}
	return s.paint(color, title)
}

// PaneTitle gives a plain heading a consistent inset and semantic focus color.
// Content casing is preserved; callers supply lowercase interface labels.
func (s Style) PaneTitle(title string, width int, focused bool) string {
	return fit(" "+s.PaneLabel(title, focused), width)
}

// SelectionColors resolves the foreground and background used for selection.
// Cell-based renderers can share Selection's palette roles without parsing SGR.
// Plain returns unset colors; missing surfaces fall back to the primary accent.
func (s Style) SelectionColors() (foreground, background theme.Color) {
	if s.Plain {
		return "", ""
	}
	background = s.Theme.Selection
	if background == "" {
		background = s.Theme.Highlight
	}
	foreground = s.Theme.Fg
	if background.BG() == "" {
		background = ""
		foreground = s.Theme.Primary
	}
	return foreground, background
}

// Selection applies a palette-defined surface and readable foreground to an
// entire selected row. Callers retain their thin pointer and domain activity
// glyphs, so selection and activity remain distinct without relying on hue.
func (s Style) Selection(text string, width int, selected bool) string {
	if s.Plain {
		return fit(ansi.Strip(text), width)
	}
	if !selected || width <= 0 {
		return fit(text, width)
	}
	foreground, background := s.SelectionColors()
	prefix := background.BG() + foreground.FG()
	if prefix == "" {
		return fit(text, width)
	}
	// Nested row styles must not reset the selection surface midway through it.
	return prefix + fit(ansi.Strip(text), width) + "\x1b[0m"
}

// Status reserves a stable right-hand cluster for small persistent indicators.
// Messages retain their semantic styling and are ellipsized before indicators
// rather than displacing them. No indicator or message is invented here.
func (s Style) Status(message, indicators string, width int) string {
	if width <= 0 {
		return ""
	}
	if s.Plain {
		message, indicators = ansi.Strip(message), ansi.Strip(indicators)
	}
	if indicators == "" {
		return fit(s.truncateStatus(message, width), width)
	}
	indicators = ansi.Truncate(indicators, width, "")
	rightWidth := ansi.StringWidth(indicators)
	leftWidth := max(0, width-rightWidth-1)
	left := s.truncateStatus(message, leftWidth)
	return fit(left, width-rightWidth) + indicators
}

func (s Style) ellipsis() string {
	if s.ASCII {
		return "..."
	}
	return "…"
}

func (s Style) truncateStatus(message string, width int) string {
	tail := s.ellipsis()
	if width <= 1 {
		tail = ""
	} else if s.ASCII && width <= 3 {
		tail = "."
	}
	return ansi.Truncate(message, width, tail)
}
