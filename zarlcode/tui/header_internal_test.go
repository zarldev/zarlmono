package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestTimelineTitleStatus_Render(t *testing.T) {
	m := New()
	m.session.Workspace = "~/proj"
	m.session.Branch = "main"
	m.session.Model = "qwen3"
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = mm.(*UI)

	out := ansi.Strip(m.View().Content)
	title := strings.SplitN(out, "\n", 2)[0]
	if !strings.HasPrefix(title, "┌") {
		t.Fatalf("top row should be the timeline border title, not a standalone header:\n%s", title)
	}
	// The timeline title carries the lowercase app/mode tokens and the model.
	// Workspace, branch, and session timing belong in the state sidebar.
	for _, want := range []string{"[" + appDisplayName + "]", "[chat]", "[qwen3]"} {
		if !strings.Contains(title, want) {
			t.Errorf("timeline title missing %q:\n%s", want, title)
		}
	}
	for _, unwanted := range []string{"~/proj", "main", "session"} {
		if strings.Contains(title, unwanted) {
			t.Errorf("timeline title should not include %q:\n%s", unwanted, title)
		}
	}
	for _, want := range []string{"enter submit", "shift+enter", "ctrl+c quit", "ctrl+q clear", "ctrl+g"} {
		if !strings.Contains(out, want) {
			t.Errorf("status missing %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"ctrl+p", "ctrl+s", "ctrl+t", "ctrl+l"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("status should not include %q:\n%s", unwanted, out)
		}
	}
}

func TestStatusHintShowsStopWhileRunning(t *testing.T) {
	m := New()
	if got := m.statusHint(); !strings.Contains(got, "ctrl+c quit") || !strings.Contains(got, "ctrl+q clear") || strings.Contains(got, "esc stop") || strings.Contains(got, "esc quit") {
		t.Fatalf("idle status hint should show ctrl+c quit and ctrl+q clear, not esc quit/stop:\n%s", got)
	}
	m.session.Run.Running = true
	if got := m.statusHint(); !strings.Contains(got, "esc stop") || !strings.Contains(got, "ctrl+c quit") || !strings.Contains(got, "ctrl+q clear") || strings.Contains(got, "esc quit") {
		t.Fatalf("running status hint should show esc stop plus ctrl+c quit and ctrl+q clear:\n%s", got)
	}
}

func TestHeaderModeBadgeUsesModeAccent(t *testing.T) {
	old := palette
	UseTheme(theme.Theme{
		Assistant: "#111111",
		Tool:      "#222222",
		PlanMode:  "#333333",
		Border:    "#444444",
	})
	t.Cleanup(func() { UseTheme(old) })

	m := New()
	if got := m.headerModeBadge(); !strings.Contains(got, theme.Color("#111111").FG()+"chat") {
		t.Fatalf("chat badge not assistant-themed: %q", got)
	}

	m.session.Run.Running = true
	if got := m.headerModeBadge(); !strings.Contains(got, theme.Color("#222222").FG()+"build") {
		t.Fatalf("build badge not tool-themed: %q", got)
	}

	m.session.PlanMode = true
	if got := m.headerModeBadge(); !strings.Contains(got, theme.Color("#333333").FG()+"plan") {
		t.Fatalf("plan badge not plan-themed: %q", got)
	}
}

func TestShortenHome(t *testing.T) {
	if got := shortenHome("/tmp/nothome"); got != "/tmp/nothome" {
		t.Errorf("non-home path changed: %q", got)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	if got := shortenHome(home); got != "~" {
		t.Errorf("home → %q, want ~", got)
	}
	if got := shortenHome(filepath.Join(home, "proj")); got != "~"+string(os.PathSeparator)+"proj" {
		t.Errorf("home subdir → %q", got)
	}
}

func TestStatusHintListsCurrentShortcuts(t *testing.T) {
	m := New()
	// Compose footer: only the essentials, ctrl+g as gateway.
	got := m.statusHint()
	for _, want := range []string{"enter submit", "shift+enter newline", "tab browse", "shift+tab plan mode", "ctrl+c quit", "ctrl+q clear", "ctrl+g"} {
		if !strings.Contains(got, want) {
			t.Fatalf("compose status hint missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"ctrl+s", "ctrl+p", "ctrl+t", "ctrl+l"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("compose status hint should not include %q:\n%s", unwanted, got)
		}
	}

	m.session.PlanMode = true
	got = m.statusHint()
	for _, want := range []string{"enter submit", "shift+tab build", "ctrl+c quit", "ctrl+q clear", "ctrl+g"} {
		if !strings.Contains(got, want) {
			t.Fatalf("plan status hint missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"ctrl+s", "ctrl+p", "ctrl+t", "ctrl+l"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("plan status hint should not include %q:\n%s", unwanted, got)
		}
	}

	m.session.PlanMode = false
	m.timeline.browsing = true
	for _, want := range []string{"↑↓/jk move", "pgup/pgdn page", "esc/i compose", "ctrl+g"} {
		if got := m.statusHint(); !strings.Contains(got, want) {
			t.Fatalf("browse status hint missing %q:\n%s", want, got)
		}
	}

	m.timeline.browsing = false
	m.session.SetCockpitExpanded(true)
	for _, want := range []string{"ctrl+l / esc / q close", "ctrl+g"} {
		if got := m.statusHint(); !strings.Contains(got, want) {
			t.Fatalf("context status hint missing %q:\n%s", want, got)
		}
	}
}
