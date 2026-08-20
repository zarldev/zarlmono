package db_test

import (
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
)

func seedToolOutputSession(t *testing.T, s *db.Store, id string) {
	t.Helper()
	if err := s.SaveSession(t.Context(), db.SessionRecord{ID: id, Workspace: "ws"}); err != nil {
		t.Fatalf("save session: %v", err)
	}
}

func TestToolOutput_RoundtripAndUpsert(t *testing.T) {
	s := openTempStore(t)
	seedToolOutputSession(t, s, "sess-1")
	ctx := t.Context()

	if err := s.SaveToolOutput(ctx, "sess-1", db.ToolOutputRecord{
		ToolCallID: "call-1", ToolName: "bash", ArgsJSON: `{"command":"echo hi"}`, Output: "hi\n",
	}); err != nil {
		t.Fatalf("save call-1: %v", err)
	}
	if err := s.SaveToolOutput(ctx, "sess-1", db.ToolOutputRecord{
		ToolCallID: "call-2", ToolName: "read", ArgsJSON: `{"path":"x"}`, Output: "content",
	}); err != nil {
		t.Fatalf("save call-2: %v", err)
	}
	// Same (session, call) id upserts — output and metadata overwrite.
	if err := s.SaveToolOutput(ctx, "sess-1", db.ToolOutputRecord{
		ToolCallID: "call-1", ToolName: "bash", ArgsJSON: `{"command":"echo bye"}`, Output: "bye\n",
	}); err != nil {
		t.Fatalf("upsert call-1: %v", err)
	}

	got, err := s.ListToolOutputsBySession(ctx, "sess-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("list len = %d, want 2", len(got))
	}
	if got[0].ToolCallID != "call-1" || got[0].Output != "bye\n" || got[0].ArgsJSON != `{"command":"echo bye"}` {
		t.Fatalf("got[0] = %+v, want upserted call-1", got[0])
	}
	if got[1].ToolCallID != "call-2" || got[1].Output != "content" {
		t.Fatalf("got[1] = %+v, want call-2", got[1])
	}

	one, err := s.GetToolOutput(ctx, "sess-1", "call-2")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if one.ToolName != "read" || one.ArgsJSON != `{"path":"x"}` {
		t.Fatalf("get = %+v", one)
	}
	if one.CreatedAt.IsZero() {
		t.Fatalf("created_at should be populated")
	}
}

func TestToolOutput_NotFound(t *testing.T) {
	s := openTempStore(t)
	seedToolOutputSession(t, s, "sess-1")

	_, err := s.GetToolOutput(t.Context(), "sess-1", "missing")
	if !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestToolOutput_CascadeDelete(t *testing.T) {
	s := openTempStore(t)
	seedToolOutputSession(t, s, "sess-1")
	ctx := t.Context()

	if err := s.SaveToolOutput(ctx, "sess-1", db.ToolOutputRecord{
		ToolCallID: "call-1", ToolName: "bash", Output: "x",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := s.DeleteSession(ctx, "sess-1"); err != nil {
		t.Fatalf("delete session: %v", err)
	}

	got, err := s.ListToolOutputsBySession(ctx, "sess-1")
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("tool outputs survived session delete: %+v", got)
	}
}
