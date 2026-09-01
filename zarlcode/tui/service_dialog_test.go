package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestServiceDialogUsesSharedDialogRegions(t *testing.T) {
	m := tui.New()
	m.OpenServiceDialog()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	out := updated.(*tui.UI).View().Content

	for _, want := range []string{"local web_search service", "SearXNG · optional local backend", "refresh status", "move", "run", "close"} {
		if !strings.Contains(out, want) {
			t.Errorf("service dialog missing %q:\n%s", want, out)
		}
	}
}

func TestServiceDialogKeepsFooterAtNarrowHeight(t *testing.T) {
	m := tui.New()
	m.OpenServiceDialog()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 8})
	lines := strings.Split(updated.(*tui.UI).View().Content, "\n")
	if !strings.Contains(lines[0], "local web_search service") {
		t.Fatalf("service dialog title missing: %q", lines[0])
	}
	if !strings.Contains(ansi.Strip(lines[len(lines)-2]), "esc close") {
		t.Fatalf("service dialog footer missing at narrow height: %q", lines[len(lines)-2])
	}
}
