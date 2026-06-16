package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestStripWide(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"plain ascii", "hello world", "hello world"},
		{"emoji presentation", "🌤️ Partly Cloudy", " Partly Cloudy"},
		{"bare astral emoji", "done 🎉", "done "},
		{"zwj sequence", "👨‍👩‍👧", ""},
		{"keeps degree + bullet", "• 19°C", "• 19°C"},
		{"keeps chrome glyphs", "▌ ✓ ✗ │ ◌ ▎", "▌ ✓ ✗ │ ◌ ▎"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripWide(tt.in); got != tt.want {
				t.Errorf("stripWide(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// After stripping, a line's rune count must equal its display width — no
// wide or zero-width graphemes remain to desync the cell grid.
func TestStripWide_WidthIsPredictable(t *testing.T) {
	for _, in := range []string{"🌤️ Partly Cloudy", "weather: ☀️ 19°C", "▌ The current weather"} {
		out := stripWide(in)
		if w, n := ansi.StringWidth(out), len([]rune(out)); w != n {
			t.Errorf("stripWide(%q) = %q: width %d != rune count %d", in, out, w, n)
		}
		if strings.ContainsRune(out, 0xFE0F) {
			t.Errorf("variation selector survived in %q", out)
		}
	}
}
