package tui_test

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func renderStatus(t *testing.T, m *tui.UI, w int) string {
	t.Helper()
	step(t, m, window(w, 8))
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	return lines[len(lines)-1]
}

func TestStatusRowQuietWhenIdleInEitherMode(t *testing.T) {
	m := tui.New()
	for _, plan := range []bool{false, true} {
		m.SetPlanMode(plan)
		for _, width := range []int{20, 80, 220} {
			if got := strings.TrimSpace(renderStatus(t, m, width)); got != "" {
				t.Fatalf("idle footer plan=%t width=%d: %q", plan, width, got)
			}
		}
	}
}

func TestStatusToastLeftAligned(t *testing.T) {
	const w = 120
	m := tui.New()
	m.SetToast("saved")
	plain := renderStatus(t, m, w)
	if !strings.Contains(plain, "saved") {
		t.Fatalf("toast missing: %q", plain)
	}
	if start := strings.Index(plain, "saved"); start != 3 {
		t.Errorf("toast text starts at %d, want inset 2 plus badge padding: %q", start, plain)
	}
}

func TestStatusToastNotDroppedWhenTooWide(t *testing.T) {
	m := tui.New()
	m.SetToast("a long notification that exceeds the bar width")
	if got := renderStatus(t, m, 20); !strings.Contains(got, "a long") {
		t.Errorf("toast dropped: %q", got)
	}
}

func TestStatusToastCollisionHasDeterministicPriority(t *testing.T) {
	m := tui.New()
	step(t, m, textKey("/"))
	m.SetErrorToast("provider unavailable")
	got := renderStatus(t, m, 20)
	if !strings.Contains(got, "provider") || strings.Contains(got, "build mode") {
		t.Fatalf("collision got %q", got)
	}
}

func TestStatusSlashSuggestionSuppressesNonExceptionalToast(t *testing.T) {
	m := tui.New()
	step(t, m, textKey("/"))
	m.SetSuccessToast("saved")
	got := renderStatus(t, m, 80)
	if !strings.Contains(got, "slash commands") || strings.Contains(got, "saved") {
		t.Fatalf("slash status got %q", got)
	}
}
