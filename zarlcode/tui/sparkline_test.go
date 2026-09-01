package tui_test

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestStackedBar(t *testing.T) {
	if got := tui.RuneWidth(tui.RenderStackedBar([]float64{1, 2, 1}, []rune{'a', 'b', 'c'}, 17)); got != 17 {
		t.Fatalf("width=%d", got)
	}
	bar := tui.RenderStackedBar([]float64{1, 1, 1}, []rune{'x', 'y', 'z'}, 10)
	if tui.RuneWidth(bar) != 10 || strings.Count(bar, "z") != 4 {
		t.Fatalf("bar=%q", bar)
	}
	if got := tui.RenderStackedBar([]float64{0}, []rune{'a'}, 10); got != "" {
		t.Fatalf("zero=%q", got)
	}
}
func TestSparkline(t *testing.T) {
	if got := tui.RuneWidth(tui.RenderSparkline([]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, 8, 0)); got != 8 {
		t.Errorf("width=%d", got)
	}
	if got := tui.RuneWidth(tui.RenderSparkline([]float64{1, 2, 3}, 8, 0)); got != 3 {
		t.Errorf("short=%d", got)
	}
	if got := tui.RenderSparkline(nil, 8, 0); got != "" {
		t.Errorf("empty=%q", got)
	}
	if r, _ := utf8.DecodeLastRuneInString(tui.RenderSparkline([]float64{0, 10}, 2, 0)); r != '█' {
		t.Errorf("peak=%q", r)
	}
}
func TestMarkThresholdPreservesANSIEscapes(t *testing.T) {
	bar := "\x1b[38;2;100;110;120m████\x1b[0m\x1b[38;2;200;210;220m░░░░\x1b[0m"
	got := tui.MarkCockpitThreshold(bar, 2)
	if !strings.Contains(got, "\x1b[38;2;100;110;120m") || !strings.Contains(got, "\x1b[38;2;200;210;220m") {
		t.Fatalf("corrupt %q", got)
	}
	short := "\x1b[38;2;100;110;120m██\x1b[0m"
	if got := tui.MarkCockpitThreshold(short, 10); got != short {
		t.Errorf("changed %q", got)
	}
}
func TestFormatters(t *testing.T) {
	cases := []struct{ got, want string }{{tui.FormatCockpitCount(42), "42"}, {tui.FormatCockpitCount(1234), "1.2k"}, {tui.FormatCockpitCount(1500000), "1.5M"}, {tui.FormatCockpitDuration(450 * time.Millisecond), "450ms"}, {tui.FormatCockpitDuration(7300 * time.Millisecond), "7.3s"}, {tui.FormatCockpitDuration(62 * time.Second), "1m02s"}, {tui.FormatCockpitUSD(.004), "$0.004"}, {tui.FormatCockpitUSD(1.27), "$1.27"}}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got %q want %q", c.got, c.want)
		}
	}
}
