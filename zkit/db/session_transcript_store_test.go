package db_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/db"
	_ "modernc.org/sqlite"
)

func TestSessionTranscriptRoundTripAndCascadeDelete(t *testing.T) {
	t.Parallel()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	update := db.TranscriptUpdate{
		SessionID: "durable-transcript", Workspace: "/workspace", Revision: 1,
		Entries: []db.TranscriptEntry{{Sequence: 1, EntryID: "e1", Kind: "user_message", PayloadJSON: []byte(`{"text":"original first turn"}`), Revision: 1}},
	}
	if err := store.UpdateActiveTranscript(t.Context(), update); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSessionTranscript(t.Context(), update.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != 1 || len(got.Entries) != 1 || got.Entries[0].EntryID != "e1" {
		t.Fatalf("transcript = %#v", got)
	}
	record, err := store.GetSession(t.Context(), update.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	record.ContextJSON = []byte(`[{"role":"user","content":"compacted"}]`)
	if err := store.SaveSession(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetSessionTranscript(t.Context(), update.SessionID)
	if err != nil || got.Revision != 1 {
		t.Fatalf("context save changed transcript: %#v %v", got, err)
	}
	if err := store.DeleteSession(t.Context(), update.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSessionTranscript(t.Context(), update.SessionID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("delete error = %v", err)
	}
}

func TestSessionSummariesHideLegacyRowsWithoutDeletingThem(t *testing.T) {
	t.Parallel()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveSession(t.Context(), db.SessionRecord{
		ID: "legacy", Workspace: "/workspace", ContextJSON: []byte(`[{"role":"user","content":"old"}]`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSessionDraft(t.Context(), db.SessionRecord{
		ID: "draft", Workspace: "/workspace", PendingJSON: []byte(`{"text":"unfinished"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateActiveTranscript(t.Context(), db.TranscriptUpdate{
		SessionID: "canonical", Workspace: "/workspace", Revision: 1,
		Entries: []db.TranscriptEntry{{Sequence: 1, EntryID: "e1", Kind: "user_message", PayloadJSON: []byte(`{"Text":"new"}`), Revision: 1}},
	}); err != nil {
		t.Fatal(err)
	}

	summaries, err := store.ListSessionSummaries(t.Context(), "/workspace")
	if err != nil {
		t.Fatal(err)
	}
	states := make(map[string]db.SessionRecord, len(summaries))
	for _, summary := range summaries {
		states[summary.ID] = summary
	}
	if states["legacy"].HasTranscript || states["legacy"].HasDraft || !states["draft"].HasDraft || !states["canonical"].HasTranscript {
		t.Fatalf("summary states = %#v", states)
	}
	if _, err := store.GetSession(t.Context(), "legacy"); err != nil {
		t.Fatalf("preserved legacy row: %v", err)
	}
}

func TestUpdateActiveTranscriptPreservesExistingSessionState(t *testing.T) {
	t.Parallel()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	created := time.Unix(1_700_000_000, 0)
	original := db.SessionRecord{
		ID: "preserve", Workspace: "/workspace", Label: "old", Provider: "provider", Model: "model",
		ContextJSON:   []byte(`[{"role":"user","content":"completed context"}]`),
		PendingJSON:   []byte(`[{"kind":"text","text":"draft"}]`),
		LastUsageJSON: []byte(`{"input_tokens":42}`), DiffBodiesJSON: []byte(`{"main.go":"diff"}`),
		PlanJSON: []byte(`{"steps":[{"text":"ship"}]}`), MessageCount: 2,
		ChangedFileCount: 3, PlanCompletedCount: 1, PlanTotalCount: 2, CreatedAt: created,
	}
	if err := store.SaveSession(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateActiveTranscript(t.Context(), db.TranscriptUpdate{
		SessionID: original.ID, Workspace: original.Workspace, Label: "new", LabelManual: true,
		AgentName: "agent", Provider: "provider-2", Model: "model-2", MessageCount: 4,
		CreatedAt: created, Revision: 1,
		Entries: []db.TranscriptEntry{{Sequence: 1, EntryID: "e1", Kind: "user_message", PayloadJSON: []byte(`{"text":"prompt"}`), Revision: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSession(t.Context(), original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.ContextJSON) != string(original.ContextJSON) || string(got.PendingJSON) != string(original.PendingJSON) ||
		string(got.LastUsageJSON) != string(original.LastUsageJSON) || string(got.DiffBodiesJSON) != string(original.DiffBodiesJSON) ||
		string(got.PlanJSON) != string(original.PlanJSON) {
		t.Fatalf("transcript update rewrote preserved state:\ngot:  %#v\nwant: %#v", got, original)
	}
	if got.ChangedFileCount != original.ChangedFileCount || got.PlanCompletedCount != original.PlanCompletedCount || got.PlanTotalCount != original.PlanTotalCount {
		t.Fatalf("transcript update rewrote summary counts: %#v", got)
	}
	if got.Label != "new" || !got.LabelManual || got.AgentName != "agent" || got.Model != "model-2" || got.MessageCount != 4 {
		t.Fatalf("transcript metadata was not updated: %#v", got)
	}
}

func TestUpdateActiveTranscriptCreatesControlledFirstSession(t *testing.T) {
	t.Parallel()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpdateActiveTranscript(t.Context(), db.TranscriptUpdate{
		SessionID: "first", Workspace: "/workspace", Label: "first turn", MessageCount: 2,
		Revision: 1,
		Entries:  []db.TranscriptEntry{{Sequence: 1, EntryID: "e1", Kind: "user_message", PayloadJSON: []byte(`{"text":"first turn"}`), Revision: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSession(t.Context(), "first")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.ContextJSON) != "[]" || string(got.PendingJSON) != "[]" || string(got.LastUsageJSON) != "null" ||
		string(got.DiffBodiesJSON) != "{}" || string(got.PlanJSON) != "null" {
		t.Fatalf("first transcript defaults = %#v", got)
	}
	active, err := store.GetSetting(t.Context(), "/workspace", "active_session")
	if err != nil {
		t.Fatal(err)
	}
	if active != "first" {
		t.Fatalf("active session = %q, want first", active)
	}
}

func TestGetSessionTranscriptReadsMetadataAndEntriesConsistently(t *testing.T) {
	t.Parallel()

	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	update := db.TranscriptUpdate{
		SessionID: "snapshot", Workspace: "/workspace", Revision: 1,
		Entries: []db.TranscriptEntry{{Sequence: 1, EntryID: "e1", Kind: "user_message", PayloadJSON: []byte(`{"text":"first"}`), Revision: 1}},
	}
	if err := store.UpdateActiveTranscript(t.Context(), update); err != nil {
		t.Fatal(err)
	}
	update.ExpectedRevision = 1
	update.Revision = 2
	update.Entries = []db.TranscriptEntry{{Sequence: 1, EntryID: "e1", Kind: "user_message", PayloadJSON: []byte(`{"text":"second"}`), Revision: 2}}

	var writers sync.WaitGroup
	writers.Add(1)
	go func() {
		defer writers.Done()
		for range 50 {
			if err := store.UpdateActiveTranscript(t.Context(), update); err == nil {
				update.ExpectedRevision = update.Revision
				update.Revision++
				update.Entries[0].Revision = update.Revision
			}
		}
	}()
	for range 100 {
		if _, err := store.GetSessionTranscript(t.Context(), update.SessionID); errors.Is(err, db.ErrTranscriptCorrupt) {
			t.Fatalf("concurrent snapshot was inconsistent: %v", err)
		} else if err != nil {
			t.Fatal(err)
		}
	}
	writers.Wait()
}

func TestUpdateActiveTranscriptRejectsInvalidRevisionAtomically(t *testing.T) {
	t.Parallel()

	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	base := db.TranscriptUpdate{
		SessionID: "invalid", Workspace: "/workspace", Revision: 1,
		Entries: []db.TranscriptEntry{{Sequence: 1, EntryID: "e1", Kind: "user_message", PayloadJSON: []byte(`{"text":"original"}`), Revision: 1}},
	}
	if err := store.UpdateActiveTranscript(t.Context(), base); err != nil {
		t.Fatal(err)
	}
	before, err := store.GetSessionTranscript(t.Context(), base.SessionID)
	if err != nil {
		t.Fatal(err)
	}

	invalid := []db.TranscriptUpdate{
		{SessionID: base.SessionID, Workspace: base.Workspace, ExpectedRevision: 1, Revision: 2},
		{SessionID: base.SessionID, Workspace: base.Workspace, ExpectedRevision: 1, Revision: 2, Entries: []db.TranscriptEntry{{Sequence: 1, EntryID: "e1", Kind: "user_message", PayloadJSON: []byte(`{"text":"zero"}`), Revision: 0}}},
		{SessionID: base.SessionID, Workspace: base.Workspace, ExpectedRevision: 1, Revision: 2, Entries: []db.TranscriptEntry{{Sequence: 1, EntryID: "e1", Kind: "user_message", PayloadJSON: []byte(`{"text":"future"}`), Revision: 3}}},
		{SessionID: base.SessionID, Workspace: base.Workspace, ExpectedRevision: 1, Revision: 3, Entries: []db.TranscriptEntry{{Sequence: 1, EntryID: "e1", Kind: "user_message", PayloadJSON: []byte(`{"text":"gap"}`), Revision: 2}}},
	}
	for _, update := range invalid {
		if err := store.UpdateActiveTranscript(t.Context(), update); err == nil {
			t.Fatalf("invalid update succeeded: %#v", update)
		}
		after, getErr := store.GetSessionTranscript(t.Context(), base.SessionID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if after.Revision != before.Revision || after.Checksum != before.Checksum || string(after.Entries[0].PayloadJSON) != string(before.Entries[0].PayloadJSON) {
			t.Fatalf("invalid update was not atomic: before=%#v after=%#v", before, after)
		}
	}
}

func TestUpdateActiveTranscriptRejectsStaleRevision(t *testing.T) {
	t.Parallel()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	first := db.TranscriptUpdate{SessionID: "stale", Workspace: "/workspace", Revision: 1, Entries: []db.TranscriptEntry{{Sequence: 1, EntryID: "e1", Kind: "user_message", PayloadJSON: []byte(`{"text":"new"}`), Revision: 1}}}
	if err := store.UpdateActiveTranscript(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	stale := first
	stale.Entries[0].PayloadJSON = []byte(`{"text":"stale"}`)
	if err := store.UpdateActiveTranscript(t.Context(), stale); !errors.Is(err, db.ErrTranscriptConflict) {
		t.Fatalf("stale update error = %v, want ErrTranscriptConflict", err)
	}
	got, err := store.GetSessionTranscript(t.Context(), "stale")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Entries[0].PayloadJSON) != `{"text":"new"}` {
		t.Fatalf("stale writer changed entry: %s", got.Entries[0].PayloadJSON)
	}
}

func TestUpdateActiveTranscriptExactReplayConflicts(t *testing.T) {
	t.Parallel()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	update := db.TranscriptUpdate{
		SessionID: "replay", Workspace: "/workspace", Revision: 1,
		Entries: []db.TranscriptEntry{{Sequence: 1, EntryID: "e1", Kind: "assistant_message", PayloadJSON: []byte(`{"text":"answer"}`), Revision: 1}},
	}
	if err := store.UpdateActiveTranscript(t.Context(), update); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateActiveTranscript(t.Context(), update); !errors.Is(err, db.ErrTranscriptConflict) {
		t.Fatalf("exact replay error = %v, want ErrTranscriptConflict", err)
	}
}

func TestCommitCompletedTurnExactReplayStillConflicts(t *testing.T) {
	t.Parallel()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	record := db.SessionRecord{ID: "commit-replay", Workspace: "/workspace", ContextJSON: []byte(`[{"role":"user","content":"first"}]`)}
	update := db.TranscriptUpdate{
		SessionID: record.ID, Workspace: record.Workspace, Revision: 1,
		Entries: []db.TranscriptEntry{{Sequence: 1, EntryID: "e1", Kind: "user_message", PayloadJSON: []byte(`{"text":"prompt"}`), Revision: 1}},
	}
	if err := store.CommitCompletedTurn(t.Context(), record, update); err != nil {
		t.Fatal(err)
	}

	record.ContextJSON = []byte(`[{"role":"user","content":"stale replacement"}]`)
	if err := store.CommitCompletedTurn(t.Context(), record, update); !errors.Is(err, db.ErrTranscriptConflict) {
		t.Fatalf("commit replay error = %v, want ErrTranscriptConflict", err)
	}
	got, err := store.GetSession(t.Context(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.ContextJSON) != `[{"role":"user","content":"first"}]` {
		t.Fatalf("commit replay changed context: %s", got.ContextJSON)
	}
}

func TestUpdateActiveTranscriptWritesOnlyChangedEntry(t *testing.T) {
	t.Parallel()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpdateActiveTranscript(t.Context(), db.TranscriptUpdate{SessionID: "delta", Workspace: "/workspace", Revision: 2, Entries: []db.TranscriptEntry{
		{Sequence: 1, EntryID: "e1", Kind: "user_message", PayloadJSON: []byte(`{"text":"fixed"}`), Revision: 1},
		{Sequence: 2, EntryID: "e2", Kind: "assistant_message", PayloadJSON: []byte(`{"text":"a"}`), Revision: 2},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateActiveTranscript(t.Context(), db.TranscriptUpdate{SessionID: "delta", Workspace: "/workspace", ExpectedRevision: 2, Revision: 3, Entries: []db.TranscriptEntry{
		{Sequence: 2, EntryID: "e2", Kind: "assistant_message", PayloadJSON: []byte(`{"text":"ab"}`), Revision: 3},
	}}); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSessionTranscript(t.Context(), "delta")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 2 || string(got.Entries[0].PayloadJSON) != `{"text":"fixed"}` || string(got.Entries[1].PayloadJSON) != `{"text":"ab"}` {
		t.Fatalf("transcript = %#v", got)
	}
}

func TestGetSessionTranscriptRejectsTamperedPayload(t *testing.T) {
	t.Parallel()
	databasePath := filepath.Join(t.TempDir(), "sessions.db")
	store, err := db.Open(t.Context(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpdateActiveTranscript(t.Context(), db.TranscriptUpdate{SessionID: "tamper", Workspace: "/workspace", Revision: 1, Entries: []db.TranscriptEntry{{Sequence: 1, EntryID: "e1", Kind: "user_message", PayloadJSON: []byte(`{"text":"original"}`), Revision: 1}}}); err != nil {
		t.Fatal(err)
	}
	tamperDB, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tamperDB.Close() })
	if _, err := tamperDB.ExecContext(t.Context(), `UPDATE session_transcript_entries SET payload_json = ? WHERE session_id = ?`, `{"text":"changed"}`, "tamper"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSessionTranscript(t.Context(), "tamper"); !errors.Is(err, db.ErrTranscriptCorrupt) {
		t.Fatalf("error = %v, want ErrTranscriptCorrupt", err)
	}
}

func TestTranscriptConflictRollsBackEntryAndChecksum(t *testing.T) {
	t.Parallel()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	first := db.TranscriptUpdate{SessionID: "atomic", Workspace: "/workspace", Revision: 1, Entries: []db.TranscriptEntry{{Sequence: 1, EntryID: "e1", Kind: "user_message", PayloadJSON: []byte(`{"text":"original"}`), Revision: 1}}}
	if err := store.UpdateActiveTranscript(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	before, err := store.GetSessionTranscript(t.Context(), "atomic")
	if err != nil {
		t.Fatal(err)
	}
	conflict := db.TranscriptUpdate{SessionID: "atomic", Workspace: "/workspace", ExpectedRevision: 0, Revision: 2, Entries: []db.TranscriptEntry{{Sequence: 1, EntryID: "e1", Kind: "user_message", PayloadJSON: []byte(`{"text":"corrupt"}`), Revision: 2}}}
	if err := store.UpdateActiveTranscript(t.Context(), conflict); !errors.Is(err, db.ErrTranscriptConflict) {
		t.Fatalf("error = %v", err)
	}
	after, err := store.GetSessionTranscript(t.Context(), "atomic")
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision || after.Checksum != before.Checksum || string(after.Entries[0].PayloadJSON) != string(before.Entries[0].PayloadJSON) {
		t.Fatalf("conflict was not atomic: before=%#v after=%#v", before, after)
	}
}
