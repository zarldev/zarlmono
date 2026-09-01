package tui

import "github.com/zarldev/zarlmono/zkit/tui/theme"

// palette is the active colour theme for the immediate-mode draw
// helpers. The zero value renders no colour — graceful degradation, and
// what the tests see (so substring assertions stay colour-agnostic). Set
// once at startup via UseTheme, before the program runs.
var palette theme.Theme

// themeGen counts theme changes. Caches that bake palette colours into
// pre-rendered strings (the timeline) include it in their key, so a live
// theme switch invalidates frozen content and it recolours on the next draw.
var themeGen uint64

// UseTheme sets the active colour theme for all subsequent draws and bumps
// the theme generation so colour-baking caches re-render.
func UseTheme(t theme.Theme) {
	palette = t
	themeGen++
}
