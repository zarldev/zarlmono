package tui

import (
	"testing"
	"time"
	"unicode/utf8"
)

// width counts display cells of an uncoloured render — every glyph the
// cockpit primitives draw (blocks, shades, rules) is single-width, so a rune
// count is the cell count when no theme is set.
func width(s string) int { return utf8.RuneCountInString(s) }

func TestStackedBar(t *testing.T) {
	t.Run("sums to exact width", func(t *testing.T) {
		bar := stackedBar([]barSeg{
			{weight: 1, glyph: 'a'},
			{weight: 2, glyph: 'b'},
			{weight: 1, glyph: 'c'},
		}, 17)
		if got := width(bar); got != 17 {
			t.Fatalf("width = %d, want 17", got)
		}
	})
	t.Run("last segment absorbs rounding", func(t *testing.T) {
		// 3 equal weights over 10 cells: 3+3+remainder(4).
		bar := stackedBar([]barSeg{
			{weight: 1, glyph: 'x'},
			{weight: 1, glyph: 'y'},
			{weight: 1, glyph: 'z'},
		}, 10)
		if got := width(bar); got != 10 {
			t.Fatalf("width = %d, want 10", got)
		}
		if countRune(bar, 'z') != 4 {
			t.Fatalf("last seg = %d, want 4 (remainder)", countRune(bar, 'z'))
		}
	})
	t.Run("zero total renders empty", func(t *testing.T) {
		if got := stackedBar([]barSeg{{weight: 0, glyph: 'a'}}, 10); got != "" {
			t.Fatalf("want empty, got %q", got)
		}
	})
}

func TestSparkline(t *testing.T) {
	t.Run("renders exactly width points", func(t *testing.T) {
		vals := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
		if got := width(sparkline(vals, 8, 0, "", "", nil)); got != 8 {
			t.Fatalf("width = %d, want 8 (last 8 of 10)", got)
		}
	})
	t.Run("short series renders all points", func(t *testing.T) {
		if got := width(sparkline([]float64{1, 2, 3}, 8, 0, "", "", nil)); got != 3 {
			t.Fatalf("width = %d, want 3", got)
		}
	})
	t.Run("empty series is empty", func(t *testing.T) {
		if got := sparkline(nil, 8, 0, "", "", nil); got != "" {
			t.Fatalf("want empty, got %q", got)
		}
	})
	t.Run("max value maps to top block", func(t *testing.T) {
		// normMax=0 auto-scales: the largest value should be the full block.
		s := sparkline([]float64{0, 10}, 2, 0, "", "", nil)
		if r, _ := utf8.DecodeLastRuneInString(s); r != '█' {
			t.Fatalf("peak rune = %q, want █", r)
		}
	})
}

func TestMarkThreshold_CorruptsANSIEscapes(t *testing.T) {
	// Regression: markThreshold must not corrupt ANSI SGR escapes in the bar.
	// The bar carries styled segments like \x1b[38;2;R;G;Bm█...\x1b[0m.
	// When col falls inside a visible glyph, the marker replaces it cleanly.
	// When col would fall inside an escape (the old bug), we now skip the
	// escape so the visible column lands on a real glyph.

	t.Run("visible glyph replacement", func(t *testing.T) {
		// Build a bar with ANSI-styled segments; a real stackedBar would
		// use theme.Color.On() but we'll inline the escapes for clarity.
		bar := "\x1b[38;2;100;110;120m████\x1b[0m\x1b[38;2;200;210;220m░░░░\x1b[0m"
		// col=2 is the third visible glyph: second █ in the first segment.
		got := markThreshold(bar, 2)
		// The marker ┤ should appear at visible column 2.
		if !containsRuneAtVisualCol(got, 2, '┤') {
			t.Errorf("want ┤ at visual col 2, got %q", got)
		}
		// No ANSI corruption: the raw escape bytes should still be intact.
		if !containsSubstr(got, "\x1b[38;2;100;110;120m") {
			t.Errorf("first ANSI escape corrupted in %q", got)
		}
		if !containsSubstr(got, "\x1b[38;2;200;210;220m") {
			t.Errorf("second ANSI escape corrupted in %q", got)
		}
	})

	t.Run("col 0 replaces first visible glyph", func(t *testing.T) {
		bar := "\x1b[38;2;100;110;120m█░\x1b[0m"
		got := markThreshold(bar, 0)
		if !containsRuneAtVisualCol(got, 0, '┤') {
			t.Errorf("want ┤ at visual col 0, got %q", got)
		}
	})

	t.Run("col beyond visible length returns unchanged", func(t *testing.T) {
		bar := "\x1b[38;2;100;110;120m██\x1b[0m"
		got := markThreshold(bar, 10)
		if got != bar {
			t.Errorf("want unchanged bar, got %q", got)
		}
	})
}

// containsRuneAtVisualCol checks whether the rune at the nth visible (non-ANSI)
// column of s equals want. See markThreshold for the ANSI-skip contract.
func containsRuneAtVisualCol(s string, col int, want rune) bool {
	runes := []rune(s)
	vis := 0
	for pos := 0; pos < len(runes); pos++ {
		if runes[pos] == 0x1b && pos+1 < len(runes) && runes[pos+1] == '[' {
			pos += 2
			for pos < len(runes) && !isANSITerminator(runes[pos]) {
				pos++
			}
			continue
		}
		if vis == col {
			return runes[pos] == want
		}
		vis++
	}
	return false
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestFormatters(t *testing.T) {
	cases := []struct {
		got, want string
	}{
		{fmtCount(42), "42"},
		{fmtCount(1234), "1.2k"},
		{fmtCount(1_500_000), "1.5M"},
		{fmtDuration(450 * time.Millisecond), "450ms"},
		{fmtDuration(7300 * time.Millisecond), "7.3s"},
		{fmtDuration(62 * time.Second), "1m02s"},
		{fmtUSD(0.004), "$0.004"},
		{fmtUSD(1.27), "$1.27"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

func countRune(s string, target rune) int {
	n := 0
	for _, r := range s {
		if r == target {
			n++
		}
	}
	return n
}
