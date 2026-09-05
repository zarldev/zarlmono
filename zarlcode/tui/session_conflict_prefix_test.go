package tui_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestTranscriptPersistRejectsDivergentDurablePrefix(t *testing.T) {
	workspace := t.TempDir()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ui := tui.New()
	ui.SetSettings(engine.NewSettings(store, nil, nil, workspace))
	ui.SetSessionIdentity("divergent-prefix", "Divergent prefix", false, time.Now())
	ui.AddPartialTranscript("turn", "prompt", "response")
	records, err := ui.CanonicalThread().RecordsSince(0)
	if err != nil {
		t.Fatal(err)
	}
	first := records[0]
	if err := store.UpdateActiveTranscript(t.Context(), db.TranscriptUpdate{
		SessionID: "divergent-prefix", Workspace: workspace, Revision: first.Revision,
		Entries: []db.TranscriptEntry{{Sequence: first.Sequence, EntryID: first.ID, ParentID: first.ParentID, TurnID: first.TurnID, Kind: first.Kind, PayloadJSON: []byte(`{"text":"divergent prefix"}`), Revision: first.Revision}},
	}); err != nil {
		t.Fatal(err)
	}
	cmd := ui.ForceTranscriptPersist()
	if cmd == nil {
		t.Fatal("transcript persistence returned no command")
	}
	ui.Update(cmd())
	stored, err := store.GetSessionTranscript(t.Context(), "divergent-prefix")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Revision != first.Revision || string(stored.Entries[0].PayloadJSON) != `{"text":"divergent prefix"}` {
		t.Fatalf("divergent durable prefix changed: %#v", stored)
	}
}
