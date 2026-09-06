package tui_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestTranscriptPersistRejectsDivergentEqualRevision(t *testing.T) {
	workspace := t.TempDir()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ui := tui.New()
	ui.SetSettings(engine.NewSettings(store, nil, nil, workspace))
	ui.SetSessionIdentity("equal-revision", "Equal revision", false, time.Now())
	ui.AddPartialTranscript("turn", "prompt", "response")
	records, err := ui.CanonicalThread().RecordsSince(0)
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]db.TranscriptEntry, len(records))
	for i, record := range records {
		entries[i] = db.TranscriptEntry{Sequence: record.Sequence, EntryID: record.ID, ParentID: record.ParentID, TurnID: record.TurnID, Kind: record.Kind, PayloadJSON: record.Payload, Revision: record.Revision}
	}
	entries[0].PayloadJSON = []byte(`{"text":"durable divergence"}`)
	if err := store.UpdateActiveTranscript(t.Context(), db.TranscriptUpdate{SessionID: "equal-revision", Workspace: workspace, Revision: ui.CanonicalThread().Revision(), Entries: entries}); err != nil {
		t.Fatal(err)
	}
	cmd := ui.ForceTranscriptPersist()
	if cmd == nil {
		t.Fatal("transcript persistence returned no command")
	}
	ui.Update(cmd())
	stored, err := store.GetSessionTranscript(t.Context(), "equal-revision")
	if err != nil {
		t.Fatal(err)
	}
	if string(stored.Entries[0].PayloadJSON) != `{"text":"durable divergence"}` {
		t.Fatalf("divergent durable prefix was overwritten: %s", stored.Entries[0].PayloadJSON)
	}
	status := renderStatus(t, ui, 120)
	if strings.Contains(status, "transcript revision conflict") || strings.Contains(status, "durable transcript diverges") {
		t.Fatalf("status exposed internal transcript conflict: %q", status)
	}
	if !strings.Contains(status, "session changed elsewhere") {
		t.Fatalf("status did not summarize transcript conflict: %q", status)
	}
	if stored.Revision != ui.CanonicalThread().Revision() {
		t.Fatalf("revision = %d, want %d", stored.Revision, ui.CanonicalThread().Revision())
	}
}
