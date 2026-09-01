package tui_test

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/db"
)

func seedToolHistory(t *testing.T, store *db.Store) {
	t.Helper()
	if err := store.SaveSession(t.Context(), db.SessionRecord{ID: "sess-1", Workspace: "ws"}); err != nil {
		t.Fatal(err)
	}
	for _, r := range []db.ToolOutputRecord{{ToolCallID: "call-1", ToolName: "bash", ArgsJSON: `{"command":"echo hi"}`, Output: "hi\n"}, {ToolCallID: "call-2", ToolName: "read", ArgsJSON: `{"path":"x"}`, Output: "content"}} {
		if err := store.SaveToolOutput(t.Context(), "sess-1", r); err != nil {
			t.Fatal(err)
		}
	}
}
func historyUI(t *testing.T, session string) *tui.UI {
	t.Helper()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if session == "sess-1" {
		seedToolHistory(t, store)
	}
	m := tui.New()
	m.OpenToolHistory(store, session)
	step(t, m, window(120, 30))
	return m
}
func TestToolHistoryListsNewestFirst(t *testing.T) {
	m := historyUI(t, "sess-1")
	id, i := m.ToolHistorySelection()
	if id != "call-2" || i != 0 {
		t.Fatalf("selected=(%q,%d)", id, i)
	}
	step(t, m, textKey("j"))
	id, i = m.ToolHistorySelection()
	if id != "call-1" || i != 1 {
		t.Fatalf("older=(%q,%d)", id, i)
	}
	step(t, m, textKey("k"))
	id, i = m.ToolHistorySelection()
	if id != "call-2" || i != 0 {
		t.Fatalf("newest=(%q,%d)", id, i)
	}
}
func TestToolHistoryShowsFullOutput(t *testing.T) {
	out := ansi.Strip(historyUI(t, "sess-1").View().Content)
	for _, w := range []string{"tool history", "2 calls", "bash", "read", "content"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q:\n%s", w, out)
		}
	}
}
func TestToolHistoryNarrowHeightKeepsSelectedCallAndFooterVisible(t *testing.T) {
	m := historyUI(t, "sess-1")
	step(t, m, textKey("j"))
	step(t, m, window(42, 7))
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "tool history") || !strings.Contains(out, "bash") || !strings.Contains(out, "esc close") {
		t.Fatalf("narrow:\n%s", out)
	}
}
func TestToolHistoryEscapeCloses(t *testing.T) {
	m := historyUI(t, "sess-1")
	step(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if strings.Contains(m.View().Content, "tool history") {
		t.Fatal("viewer remained open")
	}
}
func TestToolHistoryEmptyState(t *testing.T) {
	out := ansi.Strip(historyUI(t, "no-such-session").View().Content)
	if !strings.Contains(out, "no tool calls recorded yet") {
		t.Fatalf("empty:\n%s", out)
	}
}
