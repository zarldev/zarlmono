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

func TestStatusRowShowsOnlyContextualState(t *testing.T) {
	m := New()
	if got := renderStatus(t, m, 120); !strings.Contains(got, "build mode") || strings.Contains(got, "ctrl+") {
		t.Fatalf("idle status should show durable workflow mode without shortcut wallpaper, got %q", got)
	}

	m.session.Run.Running = true
	if got := renderStatus(t, m, 120); !strings.Contains(got, "build mode") || strings.Contains(got, "running") || strings.Contains(got, "ctrl+") {
		t.Errorf("footer should keep durable mode and leave live state to the transcript:\n%q", got)
	}
	m.session.Run.Running = false
	m.session.PlanMode = true
	if got := renderStatus(t, m, 120); !strings.Contains(got, "plan mode") || strings.Contains(got, "shift+tab") {
		t.Errorf("plan status should be concise and contextual:\n%q", got)
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

func TestStatusToastCollisionHasDeterministicPriority(t *testing.T) {
	m := New()
	m.session.SetErrorToast("provider unavailable")
	plain := renderStatus(t, m, 20)
	if !strings.Contains(plain, "provider") || strings.Contains(plain, "build mode") {
		t.Fatalf("narrow error toast should replace durable mode, got %q", plain)
	}
}

func TestStatusSlashSuggestionSuppressesNonExceptionalToast(t *testing.T) {
	m := New()
	m.composer.setText("/")
	m.session.SetSuccessToast("saved")
	plain := renderStatus(t, m, 80)
	if !strings.Contains(plain, "slash commands") || strings.Contains(plain, "saved") {
		t.Fatalf("slash discovery should own the contextual row over info toast, got %q", plain)
	}
}
