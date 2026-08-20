package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zkit/db"
)

func seedToolHistory(t *testing.T, store *db.Store, sessionID string) {
	t.Helper()
	if err := store.SaveSession(t.Context(), db.SessionRecord{ID: sessionID, Workspace: "ws"}); err != nil {
		t.Fatalf("save session: %v", err)
	}
	if err := store.SaveToolOutput(t.Context(), sessionID, db.ToolOutputRecord{
		ToolCallID: "call-1", ToolName: "bash", ArgsJSON: `{"command":"echo hi"}`, Output: "hi\n",
	}); err != nil {
		t.Fatalf("save output: %v", err)
	}
	if err := store.SaveToolOutput(t.Context(), sessionID, db.ToolOutputRecord{
		ToolCallID: "call-2", ToolName: "read", ArgsJSON: `{"path":"x"}`, Output: "content",
	}); err != nil {
		t.Fatalf("save output: %v", err)
	}
}

func TestToolHistory_ListsNewestFirst(t *testing.T) {
	s := newTestSettings(t)
	seedToolHistory(t, s.Store, "sess-1")

	h := newToolHistory(s.Store, "sess-1")
	if len(h.summaries) != 2 {
		t.Fatalf("records = %d, want 2", len(h.summaries))
	}
	if h.summaries[0].ToolCallID != "call-2" || h.summaries[1].ToolCallID != "call-1" {
		t.Fatalf("order = [%s, %s], want [call-2, call-1]", h.summaries[0].ToolCallID, h.summaries[1].ToolCallID)
	}
	if h.cursor != 0 {
		t.Fatalf("cursor = %d, want 0 (newest)", h.cursor)
	}
}

func TestToolHistory_ShowsFullOutput(t *testing.T) {
	s := newTestSettings(t)
	seedToolHistory(t, s.Store, "sess-1")

	h := newToolHistory(s.Store, "sess-1")
	buf := uv.NewScreenBuffer(120, 30)
	h.draw(buf, buf.Bounds())
	out := ansi.Strip(buf.Render())

	for _, want := range []string{"tool history", "2 calls", "bash", "read"} {
		if !strings.Contains(out, want) {
			t.Errorf("viewer missing %q:\n%s", want, out)
		}
	}
}

func TestToolHistory_Navigates(t *testing.T) {
	s := newTestSettings(t)
	seedToolHistory(t, s.Store, "sess-1")

	h := newToolHistory(s.Store, "sess-1")
	h.handleKey(tkey("j")) // down → older (call-1)
	if h.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", h.cursor)
	}
	h.handleKey(tkey("k")) // up → back to newest
	if h.cursor != 0 {
		t.Fatalf("cursor = %d, want 0", h.cursor)
	}
	if _, ok := h.handleKey(skey(tea.KeyEscape)).(actionClose); !ok {
		t.Fatalf("esc should close the viewer")
	}
}

func TestToolHistory_EmptyState(t *testing.T) {
	s := newTestSettings(t)

	h := newToolHistory(s.Store, "no-such-session")
	if len(h.summaries) != 0 {
		t.Fatalf("records = %d, want 0", len(h.summaries))
	}
	buf := uv.NewScreenBuffer(120, 30)
	h.draw(buf, buf.Bounds())
	out := ansi.Strip(buf.Render())
	if !strings.Contains(out, "no tool calls recorded yet") {
		t.Fatalf("empty state missing hint:\n%s", out)
	}
}
