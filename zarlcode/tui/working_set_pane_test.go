package tui_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestWorkingSetPane_RendersEmptyState(t *testing.T) {
	m := tui.New()
	stepUI(t, m, tea.WindowSizeMsg{Width: 120, Height: 32})
	stepUI(t, m, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'w'})

	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"working set", "no changed files this session", "0 files · 0 turns · 0/0 processes"} {
		if !strings.Contains(out, want) {
			t.Fatalf("working set empty state missing %q:\n%s", want, out)
		}
	}
}

func TestWorkingSetPane_UsesOpenUtilityChrome(t *testing.T) {
	m := tui.New()
	stepUI(t, m, tea.WindowSizeMsg{Width: 120, Height: 32})
	stepUI(t, m, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'w'})

	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	if len(lines) < 4 {
		t.Fatalf("working set rendered %d rows, want utility surface", len(lines))
	}
	if strings.HasPrefix(lines[0], "┌") || strings.HasPrefix(lines[len(lines)-1], "└") {
		t.Fatalf("working set should not use an outer box:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[0], "working set") || strings.Contains(lines[0], "ctrl+w close") {
		t.Fatalf("working set header should identify the surface without duplicate close help: %q", lines[0])
	}
	if !strings.Contains(lines[1], "─") || !strings.Contains(lines[2], "│") {
		t.Fatalf("working set missing utility divider or nav/detail split:\n%s", strings.Join(lines[:3], "\n"))
	}
	if !strings.Contains(lines[len(lines)-1], "esc close") {
		t.Fatalf("working set contextual footer missing close action: %q", lines[len(lines)-1])
	}
}

func TestWorkingSetPane_RendersOneFileDetail(t *testing.T) {
	m := tui.New()
	stepUI(t, m, tea.WindowSizeMsg{Width: 140, Height: 36})
	stepUI(t, m, teasink.ConversationStartedMsg{TaskID: "turn-1"})
	stepUI(t, m, teasink.DiffMsg{Path: "foo.go", Diff: "@@ -1 +1 @@\n-old\n+new"})
	stepUI(t, m, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'w'})

	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"foo.go", "+1 -1", "1 mutation", "turn #1 · turn-1", "latest diff", "new"} {
		if !strings.Contains(out, want) {
			t.Fatalf("working set one-file view missing %q:\n%s", want, out)
		}
	}
}

func TestWorkingSetPane_CoalescesMultipleEditsInFileRows(t *testing.T) {
	m := tui.New()
	stepUI(t, m, tea.WindowSizeMsg{Width: 150, Height: 36})
	stepUI(t, m, teasink.ConversationStartedMsg{TaskID: "turn-1"})
	stepUI(t, m, teasink.DiffMsg{Path: "a.go", Diff: "@@\n+a1"})
	stepUI(t, m, teasink.DiffMsg{Path: "a.go", Diff: "@@\n-a1\n+a2"})
	stepUI(t, m, teasink.DiffMsg{Path: "b.go", Diff: "@@\n+b1"})
	stepUI(t, m, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'w'})

	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"a.go", "b.go", "2 mutations", "2 mutations", "2 files · 1 turns · 0/0 processes"} {
		if !strings.Contains(out, want) {
			t.Fatalf("working set multi-file view missing %q:\n%s", want, out)
		}
	}
}

func TestWorkingSetPane_GroupsByTurn(t *testing.T) {
	m := tui.New()
	stepUI(t, m, tea.WindowSizeMsg{Width: 150, Height: 36})
	stepUI(t, m, teasink.ConversationStartedMsg{TaskID: "turn-1"})
	stepUI(t, m, teasink.DiffMsg{Path: "a.go", Diff: "@@\n+a1"})
	stepUI(t, m, teasink.DiffMsg{Path: "b.go", Diff: "@@\n+b1"})
	stepUI(t, m, teasink.ConversationEndedMsg{TaskID: "turn-1"})
	stepUI(t, m, teasink.ConversationStartedMsg{TaskID: "turn-2"})
	stepUI(t, m, teasink.DiffMsg{Path: "a.go", Diff: "@@\n-a1\n+a2"})
	stepUI(t, m, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'w'})
	stepUI(t, m, tea.KeyPressMsg{Code: tea.KeyTab})

	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"[ turns ]", "2 files · 2 turns · 0/0 processes", "turn #1", "turn #2", "2 files", "2 edits", "id: turn-1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("working set turn view missing %q:\n%s", want, out)
		}
	}
}

func TestWorkingSetPane_RendersProcessesView(t *testing.T) {
	m := tui.New()
	stepUI(t, m, tea.WindowSizeMsg{Width: 150, Height: 36})
	stepUI(t, m, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'w'})
	stepUI(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	stepUI(t, m, tea.KeyPressMsg{Code: tea.KeyTab})

	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"[ processes ]", "no background processes tracked", "0/0 processes", "background processes for this session"} {
		if !strings.Contains(out, want) {
			t.Fatalf("working set processes view missing %q:\n%s", want, out)
		}
	}
}

func TestWorkingSetPane_EnterOpensDiffBrowser(t *testing.T) {
	m := tui.New()
	stepUI(t, m, tea.WindowSizeMsg{Width: 140, Height: 36})
	stepUI(t, m, teasink.ConversationStartedMsg{TaskID: "turn-1"})
	stepUI(t, m, teasink.DiffMsg{Path: "foo.go", Diff: "@@\n+new"})
	stepUI(t, m, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'w'})
	stepUI(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"diff browser", "[ by file ]", "foo.go", "+new", "pgup/pgdn"} {
		if !strings.Contains(out, want) {
			t.Fatalf("diff browser missing %q:\n%s", want, out)
		}
	}
}
