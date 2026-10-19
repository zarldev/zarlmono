package tui

import "github.com/zarldev/zarlmono/zkit/tui/theme"

// palette is the active colour theme for the immediate-mode draw
// helpers. The zero value renders no colour — graceful degradation, and
// what the tests see (so substring assertions stay colour-agnostic). Set
// once at startup via UseTheme, before the program runs.
var palette theme.Theme

// queuedUserBackground is a secondary-accent tint of the resolved user surface.
// It is resolved with the palette, not blended in the viewport's draw loop.
var queuedUserBackground theme.Color

// themeGen counts theme changes. Caches that bake palette colours into
// pre-rendered strings (the timeline) include it in their key, so a live
// theme switch invalidates frozen content and it recolours on the next draw.
var themeGen uint64

// UseTheme sets the active colour theme for all subsequent draws and bumps
// the theme generation so colour-baking caches re-render.
func UseTheme(t theme.Theme) {
	// Blend only on theme changes, not for every viewport frame.
	t.UserBg, t.AssistantBg = t.ConversationBackgrounds()
	queuedUserBackground, _ = (theme.Theme{Bg: t.UserBg, Fg: t.Secondary}).ConversationBackgrounds()
	palette = t
	themeGen++
}

// agentHeading distinguishes response ownership without another surface colour.
func agentHeading(label string) string {
	return palette.Assistant.On("\x1b[1m" + label + "\x1b[22m")
}
