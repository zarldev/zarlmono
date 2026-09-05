package tui_test

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestResumeRejectsForeignInterruptedSessionBeforeRecoveryWrite(t *testing.T) {
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpdateActiveTranscript(t.Context(), db.TranscriptUpdate{
		SessionID: "foreign-interrupted", Workspace: "/foreign", Revision: 2,
		Entries: []db.TranscriptEntry{
			{Sequence: 1, EntryID: "user", Kind: "user_message", PayloadJSON: []byte(`{"text":"hello"}`), Revision: 1},
			{Sequence: 2, EntryID: "assistant", TurnID: "turn", Kind: "assistant_message", PayloadJSON: []byte(`{}`), Revision: 2},
		},
	}); err != nil {
		t.Fatal(err)
	}
	ui := tui.New()
	ui.SetSettings(engine.NewSettings(store, nil, nil, "/current"))
	if err := ui.ResumeSavedSession(t.Context(), "foreign-interrupted"); err == nil {
		t.Fatal("foreign workspace resume succeeded")
	}
	stored, err := store.GetSessionTranscript(t.Context(), "foreign-interrupted")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Revision != 2 {
		t.Fatalf("foreign resume wrote recovery revision %d, want 2", stored.Revision)
	}
}

func TestConcurrentResumeAcceptsAlreadyRecoveredTranscript(t *testing.T) {
	workspace := t.TempDir()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpdateActiveTranscript(t.Context(), db.TranscriptUpdate{
		SessionID: "concurrent-recovery", Workspace: workspace, Revision: 2,
		Entries: []db.TranscriptEntry{
			{Sequence: 1, EntryID: "user", Kind: "user_message", PayloadJSON: []byte(`{"text":"hello"}`), Revision: 1},
			{Sequence: 2, EntryID: "assistant", TurnID: "turn", Kind: "assistant_message", PayloadJSON: []byte(`{}`), Revision: 2},
		},
	}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			ui := tui.New()
			ui.SetSettings(engine.NewSettings(store, nil, nil, workspace))
			errs <- ui.ResumeSavedSession(t.Context(), "concurrent-recovery")
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent resume: %v", err)
		}
	}
	stored, err := store.GetSessionTranscript(t.Context(), "concurrent-recovery")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Revision != 3 {
		t.Fatalf("recovered revision = %d, want 3", stored.Revision)
	}
}
