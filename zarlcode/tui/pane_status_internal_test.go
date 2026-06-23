package tui

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

func renderStatus(t *testing.T, m *UI, w int) string {
	t.Helper()
	buf := uv.NewScreenBuffer(w, 1)
	m.statusPane.Draw(buf, buf.Bounds())
	return ansi.Strip(buf.Render())
}

func TestStatusHintReflectsMode(t *testing.T) {
	m := New()

	// Default (chat): browse + plan-mode affordances, no stop key.
	if got := renderStatus(t, m, 120); !strings.Contains(got, "tab browse") || !strings.Contains(got, "shift+tab plan mode") || strings.Contains(got, "esc stop") {
		t.Errorf("default hint wrong:\n%q", got)
	}
	// Live turn offers esc-stop.
	m.session.Run.Running = true
	if got := renderStatus(t, m, 120); !strings.Contains(got, "esc stop") {
		t.Errorf("running hint should offer 'esc stop':\n%q", got)
	}
	m.session.Run.Running = false
	// Plan mode offers shift+tab build.
	m.session.PlanMode = true
	if got := renderStatus(t, m, 120); !strings.Contains(got, "shift+tab build") {
		t.Errorf("plan hint should offer 'shift+tab build':\n%q", got)
	}
	m.session.PlanMode = false
	m.session.SetCockpitExpanded(true)
	if got := renderStatus(t, m, 120); !strings.Contains(got, "scroll") && !strings.Contains(got, "plan mode") {
		t.Errorf("status hint should offer either cockpit scroll keys or compose shortcuts when collapsed:\n%q", got)
	}
}

func TestStatusToastRightAligned(t *testing.T) {
	const w = 120
	m := New()
	m.session.SetToast("saved")

	plain := renderStatus(t, m, w)
	if !strings.Contains(plain, "saved") {
		t.Fatalf("toast missing from status bar:\n%q", plain)
	}
	end := strings.Index(plain, "saved") + len("saved")
	if end < w-3 {
		t.Errorf("toast should be right-aligned (end col ~%d), ended at %d:\n%q", w, end, plain)
	}
}

func TestStatusToastNotDroppedWhenTooWide(t *testing.T) {
	// A toast wider than the bar must still render (truncated), not vanish.
	m := New()
	m.session.SetToast("a long notification that exceeds the bar width")

	plain := renderStatus(t, m, 20)
	if !strings.Contains(plain, "a long") {
		t.Errorf("an over-wide toast should still render (truncated), not be dropped, got:\n%q", plain)
	}
}
