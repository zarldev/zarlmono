package db_test

import (
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
)

func TestStoreDraftRoundTripWithoutHistory(t *testing.T) {
	t.Parallel()

	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	record := db.SessionRecord{ID: "draft", Workspace: "/workspace", PendingJSON: []byte(`{"text":"unfinished"}`)}
	if err := store.SaveSessionDraft(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	summaries, err := store.ListSessionSummaries(t.Context(), record.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || !summaries[0].HasDraft {
		t.Fatalf("summaries after saving draft = %#v", summaries)
	}
	got, err := store.GetSession(t.Context(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.PendingJSON) != string(record.PendingJSON) || len(got.ContextJSON) == 0 {
		t.Fatalf("stored draft = %#v", got)
	}
	if err := store.ClearSessionDraft(t.Context(), record.ID); err != nil {
		t.Fatal(err)
	}
	summaries, err = store.ListSessionSummaries(t.Context(), record.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 0 {
		t.Fatalf("summaries after clearing draft = %#v, want draft-only row removed", summaries)
	}
}

func TestGetSessionReportsDraftState(t *testing.T) {
	t.Parallel()

	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveSessionDraft(t.Context(), db.SessionRecord{
		ID: "draft-state", Workspace: "/workspace", PendingJSON: []byte(`{"text":"unfinished"}`),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSession(t.Context(), "draft-state")
	if err != nil {
		t.Fatal(err)
	}
	if !got.HasDraft {
		t.Fatalf("GetSession HasDraft = false, pending = %s", got.PendingJSON)
	}
}

func TestStoreDraftUpdatePreservesConversationTimestamp(t *testing.T) {
	t.Parallel()

	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	record := db.SessionRecord{ID: "saved", Workspace: "/workspace", Label: "manual", ContextJSON: []byte(`[{"role":"user","content":"keep"}]`), MessageCount: 1}
	if err := store.SaveSession(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	before, err := store.GetSession(t.Context(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	record.PendingJSON = []byte(`{"text":"draft"}`)
	if err := store.SaveSessionDraft(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	after, err := store.GetSession(t.Context(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) || string(after.ContextJSON) != string(before.ContextJSON) || after.Label != before.Label {
		t.Fatalf("after draft = %#v; before = %#v", after, before)
	}
}

func TestClearStoredDraftPreservesSavedConversation(t *testing.T) {
	t.Parallel()

	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	record := db.SessionRecord{
		ID:           "saved-with-draft",
		Workspace:    "/workspace",
		Label:        "manual",
		ContextJSON:  []byte(`[{"role":"user","content":"keep"}]`),
		PendingJSON:  []byte(`{"text":"draft"}`),
		MessageCount: 1,
	}
	if err := store.SaveSession(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	before, err := store.GetSession(t.Context(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ClearSessionDraft(t.Context(), record.ID); err != nil {
		t.Fatal(err)
	}
	after, err := store.GetSession(t.Context(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(after.PendingJSON) != "[]" || string(after.ContextJSON) != string(before.ContextJSON) || after.Label != before.Label || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("after clear = %#v; before = %#v", after, before)
	}
}
