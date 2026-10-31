// Package chrome composes themeable, cell-fitted ANSI/BBS presentation.
// Shapes and spacing are independent of palette; callers own screen geometry.
package chrome

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

// Style supplies semantic colors and optional fallbacks for new decoration.
// A zero Theme or Plain renders without SGR. ASCII replaces decorative glyphs,
// not caller content. No terminal capability or environment is read here.
type Style struct {
	Theme theme.Theme
	ASCII bool
	Plain bool
}

func (s Style) paint(c theme.Color, text string) string {
	if s.Plain {
		return ansi.Strip(text)
	}
	return c.On(text)
}

func fit(text string, width int) string {
	if width <= 0 {
		return ""
	}
	text = ansi.Truncate(text, width, "")
	return text + strings.Repeat(" ", max(0, width-ansi.StringWidth(text)))
}

// Rule returns a thin heading occupying exactly width cells. A focused title
// gets brackets so focus remains visible without color; semantic colors also
// distinguish the focused border and label.
func (s Style) Rule(title string, width int, focused bool) string {
	if width <= 0 {
		return ""
	}
	line := "─"
	if s.ASCII {
		line = "-"
	}
	border, label := s.Theme.Border, s.Theme.Subtle
	if focused {
		border, label = s.Theme.BorderFocus, s.Theme.Primary
	}
	left := s.paint(border, line)
	if title != "" {
		if focused {
			left += s.paint(label, " ["+title+"] ")
		} else {
			left += s.paint(label, " "+title+" ")
		}
	}
	left = ansi.Truncate(left, width, "")
	return left + s.paint(border, strings.Repeat(line, max(0, width-ansi.StringWidth(left))))
}

// Strip returns a plain status strip occupying exactly width cells. The left
// content wins on small screens; the right content is pinned when both fit.
func (s Style) Strip(left, right string, width int) string {
	if width <= 0 {
		return ""
	}
	prefix := s.paint(s.Theme.Fg, left)
	if right == "" || ansi.StringWidth(prefix)+ansi.StringWidth(right)+1 > width {
		return fit(prefix, width)
	}
	suffix := s.paint(s.Theme.Subtle, right)
	return prefix + strings.Repeat(" ", width-ansi.StringWidth(prefix)-ansi.StringWidth(suffix)) + suffix
}

// Marker returns a two-cell focus/activity marker. Activity and selection have
// distinct glyphs even without color. Callers apply their row's semantic color.
func (s Style) Marker(selected, active bool) string {
	if s.ASCII {
		switch {
		case selected && active:
			return ">*"
		case selected:
			return "> "
		case active:
			return "* "
		default:
			return "  "
		}
	}
	switch {
	case selected && active:
		return "▸◆"
	case selected:
		return "▸ "
	case active:
		return "◆ "
	default:
		return "  "
	}
}
