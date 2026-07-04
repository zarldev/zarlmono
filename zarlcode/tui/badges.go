package tui

import (
	"strings"

	lg "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zkit/prefs"
)

// Badges are the shared status vocabulary for the settings surface: short,
// theme-coloured words (not glyph soup) used by both the setting rows and the
// providers list, so the two panes can't drift. They render right-aligned in
// a status column; joinBadges glues several with a dim separator.

const badgeActive = "● active"

// joinBadges joins the non-empty parts with a dim " · " separator.
func joinBadges(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, palette.Subtle.On(" · "))
}

// scopeBadge colours the precedence source a row resolved from: a workspace
// pin stands out (Info), a global default recedes (Subtle).
func scopeBadge(sc prefs.Scope) string {
	switch sc {
	case prefs.ScopeWorkspace:
		return palette.Info.On(sc.String())
	case prefs.ScopeGlobal:
		return palette.Subtle.On(sc.String())
	default:
		return palette.Muted.On(sc.String())
	}
}

// providerBadges builds a provider row's right-aligned status. The active
// marker leads and the credential state (local / key set / no key / signed in)
// trails, so the credential word stays right-flush and does NOT shift when a
// provider is (un)set active — the active marker grows to its left instead.
func providerBadges(active, custom, oauth, hasKey, usesKey bool) string {
	var parts []string
	if active {
		parts = append(parts, palette.Success.On(badgeActive))
	}
	if custom {
		parts = append(parts, palette.Warning.On("custom"))
	}
	switch {
	case oauth:
		if hasKey {
			parts = append(parts, palette.Success.On("signed in"))
		} else {
			parts = append(parts, palette.Subtle.On("sign in ↵"))
		}
	case !usesKey:
		parts = append(parts, palette.Subtle.On("local"))
	case hasKey:
		parts = append(parts, palette.Success.On("key set"))
	default:
		parts = append(parts, palette.Subtle.On("no key"))
	}
	return joinBadges(parts...)
}

// rowLayout places left flush-left and right (badges) flush-right within
// width using lipgloss JoinHorizontal. Right text is preserved on collision;
// left is truncated.
func rowLayout(left, right string, width int) string {
	if right == "" {
		return ansi.Truncate(left, width, "…")
	}
	if width < 1 {
		return ""
	}
	rightW := ansi.StringWidth(right)
	if rightW >= width {
		return ansi.Truncate(right, width, "…")
	}
	leftW := width - rightW - 1
	return lg.JoinHorizontal(lg.Top,
		lg.NewStyle().Width(leftW).Align(lg.Left).Render(ansi.Truncate(left, leftW, "…")),
		lg.NewStyle().Align(lg.Right).Render(right),
	)
}
