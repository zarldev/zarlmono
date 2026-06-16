package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestWorkingSetPane_RollbackRestoresSelectedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := New()
	m.SetWorkspace(dir, "")
	stepUI(t, m, tea.WindowSizeMsg{Width: 140, Height: 36})
	stepUI(t, m, teasink.ConversationStartedMsg{TaskID: "turn-1"})
	stepUI(t, m, teasink.DiffMsg{
		Path:   "file.txt",
		Diff:   "@@\n-before\n+after",
		Before: []byte("before\n"),
		After:  []byte("after\n"),
	})
	stepUI(t, m, teasink.ConversationEndedMsg{TaskID: "turn-1"})
	stepUI(t, m, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'w'})
	stepUI(t, m, tea.KeyPressMsg{Text: "r", Code: 'r'})

	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"Rollback turn turn-1 · file.txt?", "restore  file.txt", "y / enter"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rollback dialog missing %q:\n%s", want, out)
		}
	}
	stepUI(t, m, tea.KeyPressMsg{Text: "y", Code: 'y'})
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "before\n" {
		t.Fatalf("rolled back content = %q, want before", got)
	}
}

func TestWorkingSetPane_RollbackConflictIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := New()
	m.SetWorkspace(dir, "")
	stepUI(t, m, tea.WindowSizeMsg{Width: 140, Height: 36})
	stepUI(t, m, teasink.ConversationStartedMsg{TaskID: "turn-1"})
	stepUI(t, m, teasink.DiffMsg{
		Path:   "file.txt",
		Diff:   "@@\n-before\n+after",
		Before: []byte("before\n"),
		After:  []byte("after\n"),
	})
	stepUI(t, m, teasink.ConversationEndedMsg{TaskID: "turn-1"})
	if err := os.WriteFile(path, []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stepUI(t, m, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'w'})
	stepUI(t, m, tea.KeyPressMsg{Text: "r", Code: 'r'})

	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"conflict detected", "rollback is refused"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rollback conflict dialog missing %q:\n%s", want, out)
		}
	}
	stepUI(t, m, tea.KeyPressMsg{Text: "y", Code: 'y'})
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "dirty\n" {
		t.Fatalf("conflicted rollback content = %q, want dirty", got)
	}
}

func TestDiffBrowser_RollbackSelectedTurn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := New()
	m.SetWorkspace(dir, "")
	stepUI(t, m, tea.WindowSizeMsg{Width: 140, Height: 36})
	stepUI(t, m, teasink.ConversationStartedMsg{TaskID: "turn-1"})
	stepUI(t, m, teasink.DiffMsg{
		Path:   "file.txt",
		Diff:   "@@\n-before\n+after",
		Before: []byte("before\n"),
		After:  []byte("after\n"),
	})
	stepUI(t, m, teasink.ConversationEndedMsg{TaskID: "turn-1"})
	stepUI(t, m, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'w'})
	stepUI(t, m, tea.KeyPressMsg{Code: tea.KeyTab}) // turn view
	stepUI(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	stepUI(t, m, tea.KeyPressMsg{Text: "r", Code: 'r'})

	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "Rollback turn turn-1?") {
		t.Fatalf("diff browser rollback dialog missing:\n%s", out)
	}
}
