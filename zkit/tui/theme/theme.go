// Package theme is the lipgloss-free, JSON-driven colour theme for the
// v2 TUI. It loads the same theme JSON schema as pkg/tui/theme (the v1
// system) but resolves each colour to its dark hex and emits truecolor
// ANSI directly — no lipgloss, stdlib only — so it compiles under both
// x/ansi lines (root v1 at 0.10.x and the v2 module at 0.11.x) and adds
// nothing to either dependency graph.
//
// Import it aliased, since the package dir is "v2":
//
//	"github.com/zarldev/zarlmono/zkit/tui/theme"
package theme

import (
	"fmt"
	"strings"
)

const reset = "\x1b[0m"

// Color is a hex colour ("#rrggbb" or "#rgb"). The empty value renders
// nothing (no escape), so unset slots degrade to the terminal default.
type Color string

// FG returns the truecolor SGR foreground sequence, or "" if unset/invalid.
func (c Color) FG() string { return c.sgr(38) }

// BG returns the truecolor SGR background sequence, or "" if unset/invalid.
func (c Color) BG() string { return c.sgr(48) }

func (c Color) sgr(base int) string {
	r, g, b, ok := c.rgb()
	if !ok {
		return ""
	}
	return fmt.Sprintf("\x1b[%d;2;%d;%d;%dm", base, r, g, b)
}

// On wraps s in this foreground colour and a reset. No-op when unset.
func (c Color) On(s string) string {
	fg := c.FG()
	if fg == "" {
		return s
	}
	return fg + s + reset
}

func (c Color) rgb() (int, int, int, bool) {
	h := strings.TrimPrefix(strings.TrimSpace(string(c)), "#")
	switch len(h) {
	case 3: // #rgb → #rrggbb
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	case 6:
	default:
		return 0, 0, 0, false
	}
	var r, g, b int
	if n, err := fmt.Sscanf(h, "%02x%02x%02x", &r, &g, &b); err != nil || n != 3 {
		return 0, 0, 0, false
	}
	return r, g, b, true
}

// Theme is a resolved (dark-variant) colour set. Slots mirror the v1
// theme so the JSON is interchangeable.
type Theme struct {
	Name string

	// Surfaces.
	Bg, Fg, Subtle, Muted Color
	// Semantic accents.
	Primary, Secondary            Color
	Success, Warning, Error, Info Color
	// Conversation roles.
	User, Assistant, Tool, System Color
	// Structure.
	Border, BorderFocus            Color
	Highlight, Selection, PlanMode Color
}
