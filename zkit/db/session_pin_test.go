package db_test

import (
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/db"
)

func TestStoreSetSessionPinnedPreservesSession(t *testing.T) {
	t.Parallel()

	store := openTempStore(t)
	record := db.SessionRecord{
		ID:           "pin-me",
		Workspace:    "/workspace",
		Label:        "keep label",
		HistoryJSON:  []byte(`[{"role":"user","content":"keep history"}]`),
		MessageCount: 1,
	}
	if err := store.SaveSession(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	before, err := store.GetSession(t.Context(), record.ID)
	if err != nil {
		t.Fatal(err)
	}

	pinnedAt := time.Unix(1_700_000_123, 0)
	if err := store.SetSessionPinned(t.Context(), record.ID, true, pinnedAt); err != nil {
		t.Fatal(err)
	}
	pinned, err := store.GetSession(t.Context(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !pinned.Pinned || !pinned.PinnedAt.Equal(pinnedAt) {
		t.Fatalf("pin state = pinned %v at %v", pinned.Pinned, pinned.PinnedAt)
	}
	assertSessionPinPreserved(t, before, pinned)

	if err := store.SetSessionPinned(t.Context(), record.ID, false, time.Time{}); err != nil {
		t.Fatal(err)
	}
	unpinned, err := store.GetSession(t.Context(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unpinned.Pinned || !unpinned.PinnedAt.IsZero() {
		t.Fatalf("unpin state = pinned %v at %v", unpinned.Pinned, unpinned.PinnedAt)
	}
	assertSessionPinPreserved(t, before, unpinned)
}

func TestStoreListSessionSummariesOrdersPinsBeforeRecentSessions(t *testing.T) {
	t.Parallel()

	store := openTempStore(t)
	ctx := t.Context()
	mustSave(t, store, db.SessionRecord{ID: "unpinned-old", Workspace: "ws"})
	mustSave(t, store, db.SessionRecord{ID: "unpinned-new", Workspace: "ws"})
	mustSave(t, store, db.SessionRecord{ID: "pinned-old", Workspace: "ws"})
	mustSave(t, store, db.SessionRecord{ID: "pinned-new", Workspace: "ws"})

	if err := store.SetSessionPinned(ctx, "pinned-old", true, time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionPinned(ctx, "pinned-new", true, time.Unix(200, 0)); err != nil {
		t.Fatal(err)
	}

	summaries, err := store.ListSessionSummaries(ctx, "ws")
	if err != nil {
		t.Fatal(err)
	}
	wantPinned := []string{"pinned-new", "pinned-old"}
	if len(summaries) != 4 {
		t.Fatalf("summary count = %d, want 4", len(summaries))
	}
	for i, id := range wantPinned {
		if summaries[i].ID != id {
			t.Fatalf("pinned order[%d] = %q, want %q; summaries = %#v", i, summaries[i].ID, id, summaries)
		}
	}
	unpinned := map[string]bool{summaries[2].ID: true, summaries[3].ID: true}
	if !unpinned["unpinned-old"] || !unpinned["unpinned-new"] {
		t.Fatalf("unpinned summaries = %v, want old and new sessions", unpinned)
	}
}

func assertSessionPinPreserved(t *testing.T, before, after db.SessionRecord) {
	t.Helper()
	if after.Label != before.Label || string(after.HistoryJSON) != string(before.HistoryJSON) || after.MessageCount != before.MessageCount || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("pin changed session content: before = %#v; after = %#v", before, after)
	}
}
