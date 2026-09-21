package db_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestToolOutputHistoryClassificationRoundTrip(t *testing.T) {
	store := openTempStore(t)
	seedToolOutputSession(t, store, "history-classification")

	want := db.ToolOutputHistory{
		ToolOutputRecord: db.ToolOutputRecord{
			ToolCallID: "call-1",
			ToolName:   "bash",
			ArgsJSON:   `{"command":"CANARY-command"}`,
			Output:     "CANARY-late-output",
		},
		ExecutionID:    "execution-1",
		ParametersJSON: `{"command":"CANARY-command"}`,
		PartsJSON:      `[]`,
		EffectsJSON:    `[]`,
		Success:        false,
		Error:          "CANARY-terminal-timeout",
		Kind:           tools.Kinds.TRANSIENT,
	}
	if err := store.CaptureToolOutput(t.Context(), "history-classification", want); err != nil {
		t.Fatalf("capture: %v", err)
	}

	history, err := store.ListToolOutputHistory(t.Context(), "history-classification")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history length = %d, want 1", len(history))
	}
	got := history[0]
	if got.Success || got.Error != want.Error || got.Kind != tools.Kinds.TRANSIENT {
		t.Fatalf("classification = {success:%v error:%q kind:%v}, want {success:false error:%q kind:%v}", got.Success, got.Error, got.Kind, want.Error, tools.Kinds.TRANSIENT)
	}
	if got.Output != want.Output || got.ArgsJSON != want.ArgsJSON || got.ParametersJSON != want.ParametersJSON {
		t.Fatalf("raw history changed: %+v", got)
	}
}

func TestToolOutputHistoryDeleteSessionCascadesProjectionAndHistory(t *testing.T) {
	store := openTempStore(t)
	seedToolOutputSession(t, store, "history-delete")
	ctx := t.Context()

	if err := store.CaptureToolOutput(ctx, "history-delete", db.ToolOutputHistory{
		ToolOutputRecord: db.ToolOutputRecord{
			ToolCallID: "call-1",
			ToolName:   "bash",
			Output:     "captured",
		},
		Kind: tools.Kinds.TRANSIENT,
	}); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if err := store.DeleteSession(ctx, "history-delete"); err != nil {
		t.Fatalf("delete session: %v", err)
	}

	projection, err := store.ListToolOutputsBySession(ctx, "history-delete")
	if err != nil {
		t.Fatalf("list projection after delete: %v", err)
	}
	if len(projection) != 0 {
		t.Fatalf("projection survived session delete: %+v", projection)
	}
	history, err := store.ListToolOutputHistory(ctx, "history-delete")
	if err != nil {
		t.Fatalf("list history after delete: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("history survived session delete: %+v", history)
	}
}

func TestToolOutputHistoryProjectionFailureRollsBackCapture(t *testing.T) {
	store := openTempStore(t)
	seedToolOutputSession(t, store, "history-rollback")
	ctx := t.Context()

	previous := db.ToolOutputHistory{
		ToolOutputRecord: db.ToolOutputRecord{
			ToolCallID: "call-1",
			ToolName:   "bash",
			ArgsJSON:   `{"command":"previous"}`,
			Output:     "previous-output",
		},
		ExecutionID: "execution-1",
		Kind:        tools.Kinds.TRANSIENT,
	}
	if err := store.CaptureToolOutput(ctx, "history-rollback", previous); err != nil {
		t.Fatalf("capture previous: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx, `CREATE TRIGGER reject_tool_output_projection BEFORE INSERT ON tool_outputs WHEN NEW.output = 'rejected-output' BEGIN SELECT RAISE(ABORT, 'injected projection failure'); END`); err != nil {
		t.Fatalf("create projection failure trigger: %v", err)
	}

	rejected := previous
	rejected.ExecutionID = "execution-2"
	rejected.ArgsJSON = `{"command":"rejected"}`
	rejected.Output = "rejected-output"
	if err := store.CaptureToolOutput(ctx, "history-rollback", rejected); err == nil {
		t.Fatal("capture with rejected projection: got nil error")
	}

	projection, err := store.ListToolOutputsBySession(ctx, "history-rollback")
	if err != nil {
		t.Fatalf("list projection: %v", err)
	}
	if len(projection) != 1 || projection[0].Output != previous.Output || projection[0].ArgsJSON != previous.ArgsJSON {
		t.Fatalf("projection after rollback = %+v, want previous capture", projection)
	}
	history, err := store.ListToolOutputHistory(ctx, "history-rollback")
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	if len(history) != 1 || history[0].ExecutionID != previous.ExecutionID || history[0].Output != previous.Output || history[0].ArgsJSON != previous.ArgsJSON {
		t.Fatalf("history after rollback = %+v, want previous capture", history)
	}
}
