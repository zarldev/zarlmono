package tui_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

// TestSaveSessionAfterTranscriptAlreadyPersisted reproduces the turn-end
// regression: the immediate TurnFinished transcript persist advances the
// durable revision first, so the subsequent full snapshot has no transcript
// delta and must still commit the model context, usage, diffs, plan, and
// draft blobs.
func TestSaveSessionAfterTranscriptAlreadyPersisted(t *testing.T) {
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
	live.RestoreContext([]llm.Message{{Role: llm.RoleAssistant, Content: "prior context"}})

	ui := tui.New()
	ui.SetLiveRunner(live)
	ui.SetSettings(engine.NewSettings(store, nil, nil, workspaceRoot))
	ui.SetSessionIdentity("turn-end", "Turn end", false, time.Now())
	ui.AddPartialTranscript("turn", "prompt", "partial response")

	if cmd := ui.ForceTranscriptPersist(); cmd == nil {
		t.Fatal("transcript persistence returned no command")
	} else if _, next := ui.Update(cmd()); next != nil {
		t.Fatal("unexpected queued persistence operation")
	}

	if err := ui.SaveSession(t.Context()); err != nil {
		t.Fatalf("SaveSession after transcript persist: %v", err)
	}

	record, err := store.GetSession(t.Context(), "turn-end")
	if err != nil {
		t.Fatal(err)
	}
	if string(record.ContextJSON) == "" || string(record.ContextJSON) == "[]" {
		t.Fatalf("turn-end context not saved: %s", record.ContextJSON)
	}
	if string(record.LastUsageJSON) == "" || string(record.LastUsageJSON) == "null" {
		t.Fatalf("turn-end usage not saved: %s", record.LastUsageJSON)
	}
}
