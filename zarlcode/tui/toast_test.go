package tui_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestStatusToastUsesThemeSurfaceAndSemanticForeground(t *testing.T) {
	t.Cleanup(func() { tui.UseTheme(theme.Theme{}) })
	tui.UseTheme(theme.Theme{Fg: "#ffffff", Subtle: "#aaaaaa", Highlight: "#222222", Success: "#00ff00", Error: "#ff0000"})
	for _, tc := range []struct{ text, tone, fg string }{{"saved settings", "success", "#00ff00"}, {"boom", "error", "#ff0000"}, {"working…", "info", "#aaaaaa"}} {
		got := tui.RenderFooterToast(tc.text, tc.tone)
		if want := theme.Color("#222222").BG(); !strings.Contains(got, want) {
			t.Fatalf("surface missing %q in %q", want, got)
		}
		if want := theme.Color(tc.fg).FG(); !strings.Contains(got, want) {
			t.Fatalf("foreground missing %q in %q", want, got)
		}
	}
}
func TestIsErrorStatusCatchesActionFailures(t *testing.T) {
	for _, s := range []string{"name required", "transport must be 'stdio' or 'http'", "add: duplicate provider", "save key: permission denied", "fetch models: timeout", "delete: not found"} {
		if !tui.StatusLooksLikeError(s) {
			t.Fatalf("%q not error", s)
		}
	}
}
