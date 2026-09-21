package tui_test

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestToolHistoryBranchDuplicateOccurrencesAndIndependentSuffix(t *testing.T) {
	store, err := db.Open(t.Context(), t.TempDir()+"/history.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := t.Context()
	if err := store.SaveActiveSession(ctx, db.SessionRecord{ID: "source", Workspace: "ws"}); err != nil {
		t.Fatal(err)
	}
	ref, err := store.CaptureSessionHistory(ctx, "source", nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := db.SessionCheckpoint{SessionID: "source", SourceSessionID: "source", ID: "initial", Workspace: "ws",
		BoundaryID: "prompt", FormatVersion: 2, Payload: []byte(`{}`), History: ref}
	if err := store.SaveSessionCheckpoint(ctx, checkpoint); err != nil {
		t.Fatal(err)
	}
	appendTool := func(session, execution, output string) {
		t.Helper()
		data, err := runner.MarshalReplayMessage(runner.ReplayMessage{
			Message: llm.Message{Role: llm.RoleTool, ToolCallID: "repeated-call", Content: "truncated"},
			Tool: &runner.ToolOutput{ToolCallID: "repeated-call", ToolName: "read", ExecutionID: execution,
				Args: ` {"path": "` + execution + `"} `, Output: output, Success: true},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AppendSessionReplay(ctx, session, [][]byte{data}); err != nil {
			t.Fatal(err)
		}
	}
	appendTool("source", "first-execution", "FIRST-FULL-OUTPUT")
	appendTool("source", "second-execution", "SECOND-FULL-OUTPUT")
	ref, err = store.CaptureSessionHistory(ctx, "source", nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.ID, checkpoint.History = "fork", ref
	if err := store.SaveSessionCheckpoint(ctx, checkpoint); err != nil {
		t.Fatal(err)
	}
	checkpoint, err = store.GetSessionCheckpoint(ctx, "source", "fork")
	if err != nil {
		t.Fatal(err)
	}
	appendTool("source", "source-later", "FORBIDDEN-SOURCE-SUFFIX")
	if err := store.CreateCheckpointBranch(ctx, db.CheckpointBranch{
		SourceSessionID: "source", Workspace: "ws", CheckpointID: "fork", CheckpointChecksum: checkpoint.Checksum,
		Child: db.SessionRecord{ID: "child", Workspace: "ws", ContextJSON: []byte(`[]`)},
	}); err != nil {
		t.Fatal(err)
	}
	appendTool("child", "child-later", "CHILD-FULL-OUTPUT")
	// A latest-per-provider-call projection must never overwrite an occurrence.
	if err := store.SaveToolOutput(ctx, "child", db.ToolOutputRecord{ToolCallID: "repeated-call", ToolName: "read", Output: "FORBIDDEN-PROJECTION"}); err != nil {
		t.Fatal(err)
	}
	for _, sourceDeleted := range []bool{false, true} {
		if sourceDeleted {
			if err := store.DeleteSession(ctx, "source"); err != nil {
				t.Fatal(err)
			}
		}
		m := tui.New()
		m.OpenToolHistory(store, "child")
		step(t, m, window(140, 30))
		for i, want := range []string{"CHILD-FULL-OUTPUT", "SECOND-FULL-OUTPUT", "FIRST-FULL-OUTPUT"} {
			id, index := m.ToolHistorySelection()
			if id != "repeated-call" || index != i {
				t.Fatalf("deleted=%v: occurrence=(%q,%d), want index %d", sourceDeleted, id, index, i)
			}
			view := ansi.Strip(m.View().Content)
			if !strings.Contains(view, "3 occurrences") || !strings.Contains(view, want) || strings.Contains(view, "FORBIDDEN") || strings.Contains(view, "truncated") {
				t.Fatalf("deleted=%v: occurrence %d lost full output or isolation:\n%s", sourceDeleted, i, view)
			}
			step(t, m, textKey("j"))
		}
		if _, index := m.ToolHistorySelection(); index != 2 {
			t.Fatalf("unexpected extra occurrence at %d", index)
		}
	}
}

func TestToolHistoryCanonicalSourceAndInterruptedObservation(t *testing.T) {
	store, err := db.Open(t.Context(), t.TempDir()+"/history.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := t.Context()
	if err := store.SaveActiveSession(ctx, db.SessionRecord{ID: "source", Workspace: "ws"}); err != nil {
		t.Fatal(err)
	}
	ref, err := store.CaptureSessionHistory(ctx, "source", nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSessionCheckpoint(ctx, db.SessionCheckpoint{
		SessionID: "source", SourceSessionID: "source", ID: "initial", Workspace: "ws",
		BoundaryID: "prompt", FormatVersion: 2, Payload: []byte(`{}`), History: ref,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveToolOutput(ctx, "source", db.ToolOutputRecord{ToolCallID: "call", ToolName: "read", Output: "FORBIDDEN-PROJECTION"}); err != nil {
		t.Fatal(err)
	}
	view := func() string {
		t.Helper()
		m := tui.New()
		m.OpenToolHistory(store, "source")
		step(t, m, window(140, 30))
		return ansi.Strip(m.View().Content)
	}
	if got := view(); !strings.Contains(got, "0 occurrences") || strings.Contains(got, "FORBIDDEN") {
		t.Fatalf("empty canonical history fell back to projection:\n%s", got)
	}
	data, err := runner.MarshalReplayMessage(runner.ReplayMessage{Interrupted: true,
		Tool: &runner.ToolOutput{ToolCallID: "call", ToolName: "read", Error: "not dispatched", Output: "OBSERVED-BYTES"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendSessionReplay(ctx, "source", [][]byte{data}); err != nil {
		t.Fatal(err)
	}
	got := view()
	for _, want := range []string{"1 occurrences", "Interrupted observation", "not dispatched", "OBSERVED-BYTES"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "FORBIDDEN") {
		t.Fatalf("canonical source used projection:\n%s", got)
	}
	if err := store.AppendSessionReplay(ctx, "source", [][]byte{[]byte(`{"tool":false}`)}); err != nil {
		t.Fatal(err)
	}
	if got := view(); !strings.Contains(got, "invalid recorded occurrence") || strings.Contains(got, "OBSERVED-BYTES") || strings.Contains(got, "FORBIDDEN") {
		t.Fatalf("corrupt canonical history displayed a partial or fallback result:\n%s", got)
	}
}
