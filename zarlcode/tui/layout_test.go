package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

// render drives a resize through Update and returns the painted frame.
func render(t *testing.T, w, h int) string {
	t.Helper()
	m, _ := tui.New().Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m.View().Content
}

func TestLayout_WideShowsAllPanes(t *testing.T) {
	out := render(t, 220, 50)
	// The sidebar titles itself with its live status ("[state] [⠄ idle]"), so look
	// for "state" + "idle" rather than an old multi-pane label.
	for _, pane := range []string{"state", "idle", "editor"} {
		if !strings.Contains(out, pane) {
			t.Errorf("ultrawide layout is missing the %q pane:\n%s", pane, out)
		}
	}
	if !strings.Contains(out, "ctrl+c quit") {
		t.Errorf("status bar missing from ultrawide layout:\n%s", out)
	}
}

func TestLayout_NarrowCollapsesSidebar(t *testing.T) {
	out := render(t, 100, 50)
	if strings.Contains(out, "state") {
		t.Errorf("narrow layout should collapse the state sidebar, got:\n%s", out)
	}
	for _, pane := range []string{"idle", "editor"} {
		if !strings.Contains(out, pane) {
			t.Errorf("narrow layout is missing the %q pane:\n%s", pane, out)
		}
	}
	if !strings.Contains(out, "ctrl+c quit") {
		t.Errorf("status bar missing from narrow layout:\n%s", out)
	}
}

func TestLayout_ZeroSizeRendersEmpty(t *testing.T) {
	if out := tui.New().View().Content; out != "" {
		t.Errorf("unsized model should render empty, got %q", out)
	}
}
