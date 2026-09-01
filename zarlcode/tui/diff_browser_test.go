package tui_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func renderDiffBrowser(t *testing.T, ws *tui.WorkingSet, width, height int, keys ...tea.KeyPressMsg) string {
	t.Helper()
	m := tui.New()
	m.OpenDiffBrowser(ws)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m = updated.(*tui.UI)
	for _, key := range keys {
		updated, _ = m.Update(key)
		m = updated.(*tui.UI)
	}
	return m.View().Content
}

func seededDiffBrowserWorkingSet() *tui.WorkingSet {
	ws := tui.NewWorkingSet("/repo")
	ws.StartTurn("turn-1")
	ws.RecordDiff("a.go", "@@\n+a1")
	ws.RecordDiff("b.go", "@@\n+b1")
	ws.CompleteTurn("turn-1")
	ws.StartTurn("turn-2")
	ws.RecordDiff("a.go", "@@\n-a1\n+a2")
	return ws
}

func TestDiffBrowserSelectedEntryVisibleWhenScrolled(t *testing.T) {
	ws := tui.NewWorkingSet("/repo")
	for i := 1; i <= 30; i++ {
		id := fmt.Sprintf("turn-%d", i)
		ws.StartTurn(id)
		ws.RecordDiff(fmt.Sprintf("file%02d.go", i), "@@\n+x")
		ws.CompleteTurn(id)
	}
	out := renderDiffBrowser(t, ws, 150, 16, tea.KeyPressMsg{Code: tea.KeyEnd})
	if !strings.Contains(out, "turn #30") {
		t.Fatalf("selected final entry must stay visible:\n%s", out)
	}
	if strings.Contains(out, "turn #1 ") {
		t.Fatalf("top of overflowing list should scroll off:\n%s", out)
	}
}

func TestDiffBrowserRendersAtSharedUtilityMinimum(t *testing.T) {
	out := renderDiffBrowser(t, seededDiffBrowserWorkingSet(), 42, 7, tea.KeyPressMsg{Code: tea.KeyEnd})
	lines := strings.Split(ansi.Strip(out), "\n")
	if !strings.Contains(out, "diff browser") || !strings.Contains(out, "turn #2") {
		t.Fatalf("narrow diff browser lost header or selection:\n%s", out)
	}
	if !strings.Contains(lines[len(lines)-1], "enter/esc back") {
		t.Fatalf("narrow diff browser missing back action: %q", lines[len(lines)-1])
	}
}

func TestDiffBrowserUsesOpenUtilityChromeAndMutationOrder(t *testing.T) {
	out := renderDiffBrowser(t, seededDiffBrowserWorkingSet(), 150, 36)
	lines := strings.Split(ansi.Strip(out), "\n")
	if strings.HasPrefix(lines[0], "┌") || strings.HasPrefix(lines[len(lines)-1], "└") {
		t.Fatalf("diff browser should not use outer box:\n%s", out)
	}
	for _, want := range []string{"diff browser", "[ by turn ]", "turn #1", "turn #2", "2 files · 2 diffs", "a.go · mutation #1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("diff browser missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "turn #1") > strings.Index(out, "turn #2") {
		t.Fatalf("turns rendered out of order:\n%s", out)
	}
	if !strings.Contains(lines[len(lines)-1], "enter/esc back") {
		t.Fatalf("actions should be in contextual footer:\n%s", out)
	}
}

func TestDiffBrowserTabSwitchesToFileAndSessionPatch(t *testing.T) {
	tab := tea.KeyPressMsg{Code: tea.KeyTab}
	out := renderDiffBrowser(t, seededDiffBrowserWorkingSet(), 150, 36, tab)
	for _, want := range []string{"[ by file ]", "a.go", "b.go", "2 diffs · +2 -1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("file mode missing %q:\n%s", want, out)
		}
	}
	out = renderDiffBrowser(t, seededDiffBrowserWorkingSet(), 150, 36, tab, tab)
	for _, want := range []string{"session patch", "2 files · 3 diffs", "a.go · mutation #3"} {
		if !strings.Contains(out, want) {
			t.Fatalf("session mode missing %q:\n%s", want, out)
		}
	}
}

func TestDiffBrowserScrollsPreview(t *testing.T) {
	ws := tui.NewWorkingSet("/repo")
	ws.StartTurn("turn-1")
	var b strings.Builder
	b.WriteString("@@\n")
	for range 40 {
		b.WriteString("+line\n")
	}
	ws.RecordDiff("long.go", b.String())
	initial := renderDiffBrowser(t, ws, 100, 16)
	scrolled := renderDiffBrowser(t, ws, 100, 16, tea.KeyPressMsg{Code: tea.KeyPgDown})
	if !strings.Contains(initial, "+line") || initial == scrolled {
		t.Fatalf("pgdown should visibly scroll diff preview")
	}
}
