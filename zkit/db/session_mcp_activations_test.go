package db_test

import (
	"bytes"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/db/migrations"
)

func TestSessionMCPActivationsMigrationDefaultsLegacyRowsToEmpty(t *testing.T) {
	t.Parallel()

	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	provider, err := migrations.NewProvider(database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(t.Context(), 34); err != nil {
		t.Fatalf("migrate to prior version: %v", err)
	}
	if _, err := database.ExecContext(t.Context(), `
		INSERT INTO sessions (id, workspace, created_at, updated_at)
		VALUES ('legacy', '/workspace', 1, 1)`); err != nil {
		t.Fatalf("seed legacy session: %v", err)
	}
	if _, err := provider.UpByOne(t.Context()); err != nil {
		t.Fatalf("apply MCP activations migration: %v", err)
	}
	var got string
	if err := database.QueryRowContext(t.Context(),
		"SELECT mcp_activations_json FROM sessions WHERE id = 'legacy'").Scan(&got); err != nil {
		t.Fatalf("read migrated activation hints: %v", err)
	}
	if got != "" {
		t.Fatalf("legacy activation hints = %q, want empty", got)
	}
}

func TestSessionMCPActivationsRoundTripOwnsBytes(t *testing.T) {
	t.Parallel()

	store := openTempStore(t)
	input := []byte(`{"version":1,"tools":[{"connection":"docs","name":"search","definition_hash":"abc"}]}`)
	want := bytes.Clone(input)
	record := db.SessionRecord{
		ID: "owned-activations", Workspace: "/workspace", ContextJSON: []byte(`[]`),
		MCPActivationsJSON: input,
	}
	if err := store.SaveSession(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	input[0] = 'x'

	got, err := store.GetSession(t.Context(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.MCPActivationsJSON, want) {
		t.Fatalf("activation hints = %q, want %q", got.MCPActivationsJSON, want)
	}
	got.MCPActivationsJSON[0] = 'y'
	listed, err := store.ListSessions(t.Context(), record.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || !bytes.Equal(listed[0].MCPActivationsJSON, want) {
		t.Fatalf("listed activation hints = %#v, want %q", listed, want)
	}
	listed[0].MCPActivationsJSON[0] = 'z'
	again, err := store.GetSession(t.Context(), record.ID)
	if err != nil || !bytes.Equal(again.MCPActivationsJSON, want) {
		t.Fatalf("stored activation hints alias returned bytes: %q, %v", again.MCPActivationsJSON, err)
	}
}

func TestPartialSessionWritesPreserveMCPActivations(t *testing.T) {
	t.Parallel()

	store := openTempStore(t)
	want := []byte(`{"version":1,"tools":[]}`)
	record := db.SessionRecord{
		ID: "partial-activations", Workspace: "/workspace", ContextJSON: []byte(`[]`),
		PendingJSON: []byte(`{"text":"draft"}`), MCPActivationsJSON: want,
	}
	if err := store.SaveSession(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSessionDraft(t.Context(), db.SessionRecord{
		ID: record.ID, Workspace: record.Workspace, PendingJSON: []byte(`{"text":"changed"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.ClearSessionDraft(t.Context(), record.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateActiveTranscript(t.Context(), db.TranscriptUpdate{
		SessionID: record.ID, Workspace: record.Workspace, Revision: 1,
		Entries: []db.TranscriptEntry{{
			Sequence: 1, EntryID: "entry-1", Kind: "user_message",
			PayloadJSON: []byte(`{"text":"hello"}`), Revision: 1,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSession(t.Context(), record.ID)
	if err != nil || !bytes.Equal(got.MCPActivationsJSON, want) {
		t.Fatalf("activation hints after partial writes = %q, %v", got.MCPActivationsJSON, err)
	}
}

func TestCheckpointBranchMCPActivationsCopyAndConflict(t *testing.T) {
	t.Parallel()

	store, checkpoint := checkpointStore(t)
	checkpoint = saveCheckpoint(t, store, checkpoint)
	source, err := store.GetSession(t.Context(), checkpoint.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	staleVersion, err := store.SessionVersion(t.Context(), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	source.MCPActivationsJSON = []byte(`{"version":1,"tools":[{"name":"new"}]}`)
	if err := store.SaveSession(t.Context(), source); err != nil {
		t.Fatal(err)
	}

	branch := emptyCheckpointBranch(checkpoint)
	branch.Child.MCPActivationsJSON = []byte(`{"version":1,"tools":[{"name":"copied"}]}`)
	if _, err := store.CreateCheckpointBranchVersioned(t.Context(), branch, staleVersion); !errors.Is(err, db.ErrCheckpointConflict) {
		t.Fatalf("stale branch error = %v, want checkpoint conflict", err)
	}
	if _, err := store.GetSession(t.Context(), branch.Child.ID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("stale branch created child: %v", err)
	}

	currentVersion, err := store.SessionVersion(t.Context(), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCheckpointBranchVersioned(t.Context(), branch, currentVersion); err != nil {
		t.Fatal(err)
	}
	want := bytes.Clone(branch.Child.MCPActivationsJSON)
	branch.Child.MCPActivationsJSON[0] = 'x'
	child, err := store.GetSession(t.Context(), branch.Child.ID)
	if err != nil || !bytes.Equal(child.MCPActivationsJSON, want) {
		t.Fatalf("child activation hints = %q, %v; want %q", child.MCPActivationsJSON, err, want)
	}
}
