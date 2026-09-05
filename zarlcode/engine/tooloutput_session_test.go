package engine_test

import (
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestBackgroundProcessOutputStaysWithOriginatingSession(t *testing.T) {
	store, err := db.Open(t.Context(), t.TempDir()+"/sessions.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, id := range []string{"origin", "current"} {
		if err := store.SaveSessionDraft(t.Context(), db.SessionRecord{ID: id, Workspace: "/workspace", PendingJSON: []byte(`{"text":"draft"}`)}); err != nil {
			t.Fatal(err)
		}
	}

	current := "origin"
	sink := &engine.ToolOutputSink{Store: store, SessionID: func() string { return current }}
	effect := tools.NewProcessEffect("sleep 1", 0)
	effect.Process.Background = true
	effect.Process.ProcessID = "bash-origin"
	sink.Record(t.Context(), runner.ToolOutput{
		ToolCallID: "call", ToolName: "bash", Effects: []tools.Effect{effect},
	})

	current = "current"
	sink.Record(engine.WithToolOutputSession(t.Context(), "origin"), runner.ToolOutput{ToolCallID: "foreground", ToolName: "read", Output: "done"})
	if _, err := store.GetToolOutput(t.Context(), "origin", "foreground"); err != nil {
		t.Fatalf("originating foreground output: %v", err)
	}

	sink.RecordProcess(t.Context(), "bash-origin", "sleep 1", 0, []string{"done"}, nil)

	if _, err := store.GetToolOutput(t.Context(), "origin", "bash-origin"); err != nil {
		t.Fatalf("originating session output: %v", err)
	}
	if _, err := store.GetToolOutput(t.Context(), "current", "bash-origin"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("current session unexpectedly owns background output: %v", err)
	}
}

func TestUnownedBackgroundProcessOutputIsSkipped(t *testing.T) {
	store, err := db.Open(t.Context(), t.TempDir()+"/sessions.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveSessionDraft(t.Context(), db.SessionRecord{ID: "current", Workspace: "/workspace", PendingJSON: []byte(`{"text":"draft"}`)}); err != nil {
		t.Fatal(err)
	}

	current := "current"
	sink := &engine.ToolOutputSink{Store: store, SessionID: func() string { return current }}
	sink.RecordProcess(t.Context(), "unknown", "sleep 1", 0, []string{"done"}, nil)

	current = ""
	effect := tools.NewProcessEffect("sleep 1", 0)
	effect.Process.Background = true
	effect.Process.ProcessID = "unowned"
	sink.Record(t.Context(), runner.ToolOutput{ToolCallID: "call", ToolName: "bash", Effects: []tools.Effect{effect}})
	current = "current"
	sink.RecordProcess(t.Context(), "unowned", "sleep 1", 0, []string{"done"}, nil)

	for _, id := range []string{"unknown", "unowned"} {
		if _, err := store.GetToolOutput(t.Context(), "current", id); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("current session unexpectedly owns %q process output: %v", id, err)
		}
	}
}
