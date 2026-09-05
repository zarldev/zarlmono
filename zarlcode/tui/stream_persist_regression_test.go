package tui_test

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

// TestRepeatedStreamingPersistsNeverConflict drives many sequential transcript
// persists against one session. Any revision-accounting drift in the streaming
// path surfaces as ErrTranscriptConflict on a later write.
func TestRepeatedStreamingPersistsNeverConflict(t *testing.T) {
	workspaceRoot := t.TempDir()
	workspace, err := code.NewWorkspace(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ui := tui.New()
	ui.SetLiveRunner(engine.NewLiveRunner(nil, workspace, "test-model"))
	ui.SetSettings(engine.NewSettings(store, nil, nil, workspaceRoot))
	ui.SetSessionIdentity("stream-loop", "Stream loop", false, time.Now())

	for i := range 40 {
		ui.AddPartialTranscript("turn-"+strconv.Itoa(i), "prompt", "delta")
		cmd := ui.ForceTranscriptPersist()
		if cmd == nil {
			t.Fatalf("persist %d returned no command", i)
		}
		msg := cmd()
		if msg == nil {
			t.Fatalf("persist %d returned no message", i)
		}
		if _, next := ui.Update(msg); next != nil {
			t.Fatalf("persist %d queued an unexpected follow-up", i)
		}
	}

	if err := ui.FlushSessionPersistence(t.Context()); err != nil {
		t.Fatalf("flush after repeated streaming persists: %v", err)
	}
	stored, err := store.GetSessionTranscript(t.Context(), "stream-loop")
	if err != nil {
		t.Fatal(err)
	}
	if ui.PersistedTranscriptRevision() != stored.Revision {
		t.Fatalf("ack revision = %d, want stored %d", ui.PersistedTranscriptRevision(), stored.Revision)
	}
	if !strings.Contains(tui.TranscriptText(stored), "prompt") {
		t.Fatalf("streamed transcript lost content")
	}
}

// TestRepeatedStreamingPersistsThenSaveSession verifies the turn-end snapshot
// still commits model context after many mid-turn transcript writes.
func TestRepeatedStreamingPersistsThenSaveSession(t *testing.T) {
	workspaceRoot := t.TempDir()
	workspace, err := code.NewWorkspace(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	live := engine.NewLiveRunner(nil, workspace, "test-model")
	live.RestoreContext([]llm.Message{{Role: llm.RoleAssistant, Content: "terminal context"}})

	ui := tui.New()
	ui.SetLiveRunner(live)
	ui.SetSettings(engine.NewSettings(store, nil, nil, workspaceRoot))
	ui.SetSessionIdentity("stream-then-save", "Stream then save", false, time.Now())

	for i := range 10 {
		ui.AddPartialTranscript("turn-"+strconv.Itoa(i), "prompt", "delta")
		cmd := ui.ForceTranscriptPersist()
		if cmd == nil {
			t.Fatalf("persist %d returned no command", i)
		}
		if _, next := ui.Update(cmd()); next != nil {
			t.Fatalf("persist %d queued an unexpected follow-up", i)
		}
	}

	if err := ui.SaveSession(t.Context()); err != nil {
		t.Fatalf("SaveSession after repeated streaming persists: %v", err)
	}
	record, err := store.GetSession(t.Context(), "stream-then-save")
	if err != nil {
		t.Fatal(err)
	}
	if string(record.ContextJSON) == "" || string(record.ContextJSON) == "[]" {
		t.Fatalf("terminal context not saved after repeated persists: %s", record.ContextJSON)
	}
}
