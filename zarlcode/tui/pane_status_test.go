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

func TestStatusRowShowsOnlyContextualState(t *testing.T) {
	m := tui.New()
	if got := renderStatus(t, m, 120); !strings.Contains(got, "build mode") || strings.Contains(got, "ctrl+") {
		t.Fatalf("idle status got %q", got)
	}
	m.SetPlanMode(true)
	if got := renderStatus(t, m, 120); !strings.Contains(got, "plan mode") || strings.Contains(got, "shift+tab") {
		t.Errorf("plan status got %q", got)
	}
}

func TestStatusToastRightAligned(t *testing.T) {
	const w = 120
	m := tui.New()
	m.SetToast("saved")
	plain := renderStatus(t, m, w)
	if !strings.Contains(plain, "saved") {
		t.Fatalf("toast missing: %q", plain)
	}
	if end := strings.Index(plain, "saved") + len("saved"); end < w-3 {
		t.Errorf("toast ended at %d: %q", end, plain)
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
