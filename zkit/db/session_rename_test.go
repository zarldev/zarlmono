package db_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/db"
)

func TestStoreRenameSessionPreservesRecord(t *testing.T) {
	t.Parallel()

	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	record := db.SessionRecord{
		ID: "rename-me", Workspace: "/workspace", Label: "old label",
		HistoryJSON: []byte(`[{"role":"user","content":"keep me"}]`), MessageCount: 3,
		CreatedAt: time.Now().Add(-time.Hour),
	}
	if err := store.SaveSession(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	before, err := store.GetSession(t.Context(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RenameSession(t.Context(), record.ID, ""); err != nil {
		t.Fatal(err)
	}
	after, err := store.GetSession(t.Context(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Label != "" || !after.LabelManual || after.MessageCount != before.MessageCount || string(after.HistoryJSON) != string(before.HistoryJSON) || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("renamed session = %#v; before = %#v", after, before)
	}
}
