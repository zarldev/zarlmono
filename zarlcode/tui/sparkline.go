package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

// This file holds the cockpit's pure render primitives: block sparklines,
// pressure-coloured gauges, and proportional stacked bars. They take their
// colours as arguments (no global palette) so they stay unit-testable —
// a zero theme.Color renders the glyphs uncoloured, which is exactly what
// the substring assertions in the tests want.

// sparkBlocks is the eight-level ramp a single-row block sparkline draws
// from. Index 0 is the shortest visible mark so a non-zero sample never
// renders as blank (which would read as "no data").
var sparkBlocks = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// sparkline renders the last width points of vals as a one-row block
// sparkline. normMax fixes the top of the scale; pass 0 to auto-scale to
// the window's own maximum (good for unbounded series like tok/s) or 1 for
// an absolute 0..1 fraction (good for context-fill). c colours the line.
//
// marks, when non-nil and index-aligned with vals, recolours flagged points
// with markC — the cockpit uses it to paint a compaction dip in Warning so
// the sawtooth reads as "the window was trimmed here", not noise.
func sparkline(vals []float64, width int, normMax float64, c, markC theme.Color, marks []bool) string {
	if width <= 0 || len(vals) == 0 {
		return ""
	}
	// Window to the most recent `width` samples; keep marks aligned.
	start := 0
	if len(vals) > width {
		start = len(vals) - width
	}
	win := vals[start:]
	var winMarks []bool
	if marks != nil && len(marks) == len(vals) {
		winMarks = marks[start:]
	}

	top := normMax
	if top <= 0 {
		for _, v := range win {
			if v > top {
				top = v
			}
		}
	}
	if top <= 0 {
		top = 1 // all-zero series → flat floor, avoids divide-by-zero
	}

	var b strings.Builder
	for i, v := range win {
		level := int(v / top * float64(len(sparkBlocks)-1))
		if level < 0 {
			level = 0
		}
		if level >= len(sparkBlocks) {
			level = len(sparkBlocks) - 1
		}
		glyph := string(sparkBlocks[level])
		switch {
		case winMarks != nil && winMarks[i] && markC != "":
			b.WriteString(markC.On(glyph))
		case c != "":
			b.WriteString(c.On(glyph))
		default:
			b.WriteString(glyph)
		}
	}
	return b.String()
}

// barSeg is one coloured slice of a stacked bar: its weight (any unit; only
// the ratio matters), its colour, and the glyph to fill it with.
type barSeg struct {
	weight float64
	color  theme.Color
	glyph  rune
}

// stackedBar lays segs end-to-end across width cells, each segment sized in
// proportion to its weight. Rounding drift is absorbed by the final segment
// so the bar is always exactly width cells wide — the cockpit relies on this
// to keep the role graph flush with the gauge above it.
func stackedBar(segs []barSeg, width int) string {
	if width <= 0 || len(segs) == 0 {
		return ""
	}
	var total float64
	for _, s := range segs {
		if s.weight > 0 {
			total += s.weight
		}
	}
	if total <= 0 {
		return ""
	}

	var b strings.Builder
	used := 0
	for i, s := range segs {
		var n int
		if i == len(segs)-1 {
			n = width - used // last segment takes the remainder
		} else if s.weight > 0 {
			n = int(s.weight / total * float64(width))
		}
		if n <= 0 {
			continue
		}
		if used+n > width {
			n = width - used
		}
		glyph := s.glyph
		if glyph == 0 {
			glyph = '█'
		}
		b.WriteString(s.color.On(strings.Repeat(string(glyph), n)))
		used += n
	}
	return b.String()
}

// markThreshold overlays a pressure-threshold glyph (┤) at visual column col
// within bar. bar carries ANSI-styled segments; col counts visible glyphs only,
// skipping embedded SGR escape sequences so the marker never corrupts one.
// When col is out of range the bar is returned unchanged. The marker is
// rendered in Muted so it stands apart from the bar's filled segments.
func markThreshold(bar string, col int) string {
	if col < 0 {
		return bar
	}
	runes := []rune(bar)
	// Find the rune index for visual column col, skipping ANSI escapes.
	vis := 0
	pos := 0
	for pos < len(runes) {
		if vis == col {
			break
		}
		// Skip ANSI escape sequences: ESC [ ... terminating letter.
		if runes[pos] == 0x1b && pos+1 < len(runes) && runes[pos+1] == '[' {
			pos += 2 // skip ESC [
			for pos < len(runes) && !isANSITerminator(runes[pos]) {
				pos++
			}
			if pos < len(runes) {
				pos++ // skip the terminator itself
			}
			continue
		}
		vis++
		pos++
	}
	if pos >= len(runes) {
		return bar // col beyond visible length
	}
	runes[pos] = '┤'
	return palette.Muted.On(string(runes))
}

// isANSITerminator reports whether r ends an ANSI escape sequence parameter
// string. SGR ends at 'm'; other CSI sequences end at a letter in A-Za-z.
func isANSITerminator(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
}

// spaces returns n space characters (empty for n <= 0).
func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(" ", n)
}

// --- local formatting (textutil pulls lipgloss v1, unusable in this module) ---

// fmtCount renders n compactly: 1234 → "1.2k", 1_500_000 → "1.5M".
func fmtCount(n int) string {
	switch {
	case n < 0:
		return "-" + fmtCount(-n)
	case n < 1000:
		return strconv.Itoa(n)
	case n < 1_000_000:
		return trimZero(float64(n)/1000) + "k"
	default:
		return trimZero(float64(n)/1_000_000) + "M"
	}
}

// fmtBytes renders a byte count compactly: 512 → "512B", 1234 → "1.2KB",
// 1_500_000 → "1.5MB". 1000-based to match fmtCount's glanceable scale.
func fmtBytes(n int) string {
	switch {
	case n < 0:
		return "-" + fmtBytes(-n)
	case n < 1000:
		return strconv.Itoa(n) + "B"
	case n < 1_000_000:
		return trimZero(float64(n)/1000) + "KB"
	default:
		return trimZero(float64(n)/1_000_000) + "MB"
	}
}

// trimZero formats v with one decimal, dropping a trailing ".0".
func trimZero(v float64) string {
	s := fmt.Sprintf("%.1f", v)
	return strings.TrimSuffix(s, ".0")
}

// fmtDuration renders d at a glanceable resolution: sub-second in ms,
// seconds with one decimal, minutes as "1m02s".
func fmtDuration(d time.Duration) string {
	switch {
	case d <= 0:
		return "0s"
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return trimZero(d.Seconds()) + "s"
	default:
		m := int(d / time.Minute)
		s := int((d % time.Minute) / time.Second)
		return fmt.Sprintf("%dm%02ds", m, s)
	}
}

// fmtAgo renders elapsed time as a terse relative stamp ("3m ago").
func fmtAgo(d time.Duration) string {
	switch {
	case d < time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}

// fmtUSD renders a dollar amount at a sensible precision: sub-cent in
// 3 decimals, otherwise 2 ("$0.004", "$1.27").
func fmtUSD(v float64) string {
	if v > 0 && v < 0.01 {
		return fmt.Sprintf("$%.3f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}
