package chrome_test

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zkit/tui/chrome"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestFamilyGeometryAndIndicators(t *testing.T) {
	palette, ok := theme.ByName("ansi-bbs")
	if !ok {
		t.Fatal("family fallback must ship in the catalogue")
	}
	for _, style := range []chrome.Style{{}, {Theme: palette}, {Theme: palette, Plain: true}, {Theme: palette, ASCII: true}} {
		for _, width := range []int{-1, 0, 1, 2, 5, 20, 40, 80, 100, 160} {
			for _, selected := range []bool{false, true} {
				for _, line := range []string{
					style.Selection(palette.Success.On("▸▶ café 日本語"), width, selected),
					style.PaneTitle("conversations", width, selected),
					style.Status(palette.Error.On(strings.Repeat("error 界 ", 30)), palette.Success.On("●"), width),
				} {
					if ansi.StringWidth(line) != max(0, width) {
						t.Fatalf("width %d selected=%v: %q", width, selected, line)
					}
					if style.Plain && strings.Contains(line, "\x1b") {
						t.Fatalf("plain output styled: %q", line)
					}
				}
			}
			if width > 0 {
				line := ansi.Strip(style.Status(strings.Repeat("a", 200), "●", width))
				if !strings.HasSuffix(line, "●") {
					t.Fatalf("message displaced indicator at %d: %q", width, line)
				}
			}
		}
	}
}

func TestOriginalLogoHasCommonFraming(t *testing.T) {
	const art = " /\\\\\n/___\\"
	for _, width := range []int{10, 40, 80, 100, 160} {
		rows := (chrome.Style{}).Logo(art, width, 5)
		if len(rows) != 5 || strings.TrimSpace(rows[0]) != "" || strings.TrimSpace(rows[3]) != "" || rows[4] != strings.Repeat("─", width) {
			t.Fatalf("unexpected frame: %#v", rows)
		}
		for i, original := range strings.Split(art, "\n") {
			if !strings.Contains(rows[i+1], original) || ansi.StringWidth(rows[i+1]) != width {
				t.Fatalf("original logo changed: %q", rows[i+1])
			}
		}
	}
	for _, size := range [][2]int{{1, 20}, {40, 4}, {0, 0}} {
		if rows := (chrome.Style{}).Logo(art, size[0], size[1]); len(rows) != 0 {
			t.Fatalf("logo must fit as a complete band: %v", size)
		}
	}
}

func TestSelectionIsColoredAndRetainsPlaybackInformation(t *testing.T) {
	palette, _ := theme.ByName("ansi-bbs")
	style := chrome.Style{Theme: palette}
	selected := style.Selection(palette.Success.On("▸▶ track · 3:42"), 40, true)
	if !strings.HasPrefix(selected, palette.Selection.BG()+palette.Fg.FG()) || !strings.Contains(ansi.Strip(selected), "▸▶ track · 3:42") {
		t.Fatalf("selection must retain activity and use the shared role: %q", selected)
	}
	if strings.Count(selected, "\x1b[0m") != 1 {
		t.Fatal("nested styles reset the selected surface")
	}
	other, _ := theme.ByName("retro-amber")
	changed := (chrome.Style{Theme: other}).Selection("▸▶ track · 3:42", 40, true)
	if selected == changed || ansi.Strip(selected) != ansi.Strip(changed) {
		t.Fatal("theme should change colors, not selection geometry/content")
	}
}
