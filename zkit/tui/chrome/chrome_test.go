package chrome_test

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zkit/tui/chrome"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestCellGeometry(t *testing.T) {
	palette, ok := theme.ByName("ansi-bbs")
	if !ok {
		t.Fatal("ansi-bbs must ship in the shared catalogue")
	}
	for _, style := range []chrome.Style{{}, {Theme: palette}, {Theme: palette, ASCII: true}, {Theme: palette, Plain: true}} {
		for _, width := range []int{-1, 0, 1, 2, 8, 28, 40, 48, 80, 100, 160} {
			for _, title := range []string{"", "conversations", "café / 日本語 / 🌅"} {
				for _, focused := range []bool{false, true} {
					for _, line := range []string{style.Rule(title, width, focused), style.Strip(title, "offline", width)} {
						if got := ansi.StringWidth(line); got != max(0, width) {
							t.Errorf("width %d: %q occupies %d cells", width, line, got)
						}
					}
				}
			}
		}
	}
}

func TestPaletteIndependentShapesAndFallbacks(t *testing.T) {
	a, _ := theme.ByName("ansi-bbs")
	b, _ := theme.ByName("retro-amber")
	first, second := chrome.Style{Theme: a}, chrome.Style{Theme: b}
	for _, pair := range [][2]string{
		{first.Rule("reply", 40, true), second.Rule("reply", 40, true)},
		{first.Strip("zarl / code", "plan", 80), second.Strip("zarl / code", "plan", 80)},
	} {
		if ansi.Strip(pair[0]) != ansi.Strip(pair[1]) || pair[0] == pair[1] {
			t.Fatalf("theme must recolor, not reshape: %q / %q", pair[0], pair[1])
		}
	}
	plain := chrome.Style{Theme: a, Plain: true}
	ascii := chrome.Style{ASCII: true, Plain: true}
	for _, row := range []string{plain.Rule("reply", 80, true), plain.Strip("test", "error", 80)} {
		if strings.Contains(row, "\x1b") {
			t.Fatalf("plain chrome emitted an escape: %q", row)
		}
	}
	for _, row := range []string{ascii.Rule("library", 80, true), ascii.Marker(true, true)} {
		for _, r := range row {
			if r > 127 {
				t.Fatalf("ASCII chrome contains %q", r)
			}
		}
	}
	if plain.Rule("reply", 40, true) == plain.Rule("reply", 40, false) || plain.Marker(true, false) == plain.Marker(false, true) {
		t.Fatal("focus and activity must remain distinct without color")
	}
	if got := plain.Rule("reply", 40, true); !strings.Contains(got, "[reply]") || strings.ContainsAny(got, "▓═░▒█") {
		t.Fatalf("focused rule should use thin linework and a bracketed title: %q", got)
	}
	if got := plain.Strip("left", "right", 40); strings.ContainsAny(got, "▓═░▒█") {
		t.Fatalf("strip should contain only semantic text and spacing: %q", got)
	}
}

func TestPlainStripsCallerColors(t *testing.T) {
	style := chrome.Style{Theme: theme.Theme{Primary: "#ffffff", BorderFocus: "#ff0000"}, Plain: true}
	if got, want := style.Rule("\x1b[31mgo\x1b[0m", 12, true), "─ [go] ─────"; got != want {
		t.Fatalf("colored focus title: got %q, want %q", got, want)
	}
	if got, want := style.Strip("\x1b[32mleft\x1b[0m", "\x1b[34mright\x1b[0m", 18), "left         right"; got != want {
		t.Fatalf("colored strip content: got %q, want %q", got, want)
	}
}
