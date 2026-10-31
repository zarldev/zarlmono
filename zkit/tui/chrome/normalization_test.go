package chrome_test

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/zarldev/zarlmono/zkit/tui/chrome"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestSelectionColorsMatchStringAndCellRenderers(t *testing.T) {
	for _, tc := range []struct {
		name       string
		style      chrome.Style
		foreground theme.Color
		background theme.Color
	}{
		{"selection before highlight", chrome.Style{Theme: theme.Theme{Fg: "#eef0f5", Primary: "#7799ff", Selection: "#223344", Highlight: "#995522"}}, "#eef0f5", "#223344"},
		{"legacy surface fallback", chrome.Style{Theme: theme.Theme{Fg: "#eef0f5", Highlight: "#223344"}}, "#eef0f5", "#223344"},
		{"transparent accent", chrome.Style{Theme: theme.Theme{Fg: "#eef0f5", Primary: "#7799ff"}}, "#7799ff", ""},
		{"invalid surface", chrome.Style{Theme: theme.Theme{Primary: "#7799ff", Selection: "invalid"}}, "#7799ff", ""},
		{"zero palette", chrome.Style{}, "", ""},
		{"plain", chrome.Style{Theme: theme.Theme{Fg: "#eef0f5", Selection: "#223344"}, Plain: true}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fg, bg := tc.style.SelectionColors()
			if fg != tc.foreground || bg != tc.background {
				t.Fatalf("selection colors = %q/%q, want %q/%q", fg, bg, tc.foreground, tc.background)
			}
			text := tc.style.Selection("▸▶ chosen 界", 24, true)
			if prefix := bg.BG() + fg.FG(); prefix != "" && !strings.HasPrefix(text, prefix) {
				t.Fatalf("string selection disagrees with cell colors: %q", text)
			}
			if ansi.StringWidth(text) != 24 || !strings.Contains(ansi.Strip(text), "▸▶ chosen 界") {
				t.Fatalf("selection changed content or geometry: %q", text)
			}
			if tc.style.Plain && strings.Contains(text, "\x1b") {
				t.Fatal("plain selection emits styling")
			}
		})
	}
}

func TestPaneLabelComposesWithoutPadding(t *testing.T) {
	palette := theme.Theme{Primary: "#7799ff", Subtle: "#aabbcc"}
	for _, style := range []chrome.Style{{Theme: palette}, {Theme: palette, Plain: true}, {}} {
		for _, focused := range []bool{false, true} {
			label := style.PaneLabel("context", focused)
			if got := ansi.Strip(label + " · 42 tokens"); got != "context · 42 tokens" {
				t.Fatalf("label inserts padding: %q", got)
			}
			if got := ansi.Strip(style.PaneTitle("context", 20, focused)); got != " context"+strings.Repeat(" ", 12) {
				t.Fatalf("pane title geometry changed: %q", got)
			}
			if style.Plain && strings.Contains(label, "\x1b") {
				t.Fatal("plain label emits styling")
			}
		}
	}
}
