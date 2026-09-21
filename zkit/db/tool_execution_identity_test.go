package db_test

import (
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
)

func TestToolExecutionIdentityRoundTrip(t *testing.T) {
	store := openTempStore(t)
	seedToolOutputSession(t, store, "identity")
	for i, dispatched := range []*bool{nil, new(false), new(true)} {
		want := db.ToolOutputHistory{ToolOutputRecord: db.ToolOutputRecord{ToolCallID: "reused", ToolName: "read", Output: "full"},
			ExecutionID: []string{"legacy", "denied", "executed"}[i], ParentExecutionID: "parent-execution", ParentToolCallID: "parent-call", TaskID: "task", Attempt: 3, Sequence: i, Dispatched: dispatched}
		if err := store.CaptureToolOutput(t.Context(), "identity", want); err != nil {
			t.Fatal(err)
		}
		rows, err := store.ListToolOutputHistory(t.Context(), "identity")
		if err != nil || len(rows) != i+1 {
			t.Fatalf("rows=%d, %v", len(rows), err)
		}
		got := rows[i]
		if got.ExecutionID != want.ExecutionID || got.ParentExecutionID != want.ParentExecutionID || got.ParentToolCallID != want.ParentToolCallID || got.TaskID != want.TaskID || got.Attempt != 3 || got.Sequence != i || !reflect.DeepEqual(got.Dispatched, dispatched) {
			t.Fatalf("identity = %+v", got)
		}
	}
	// Supply all pre-identity-migration columns; only the new ownership and
	// disposition columns may be omitted by an older writer.
	if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO tool_output_history
		(session_id, tool_call_id, tool_name, execution_id, parent_tool_call_id,
		 args_json, parameters_json, output, parts_json, effects_json, success, error, kind, created_at)
		VALUES ('identity', 'old', 'read', 'old-execution', 'old-parent-call',
		 '{}', '{}', 'old-output', '[]', '[]', 0, 'old-error', 'unknown', 1)`); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListToolOutputHistory(t.Context(), "identity")
	if err != nil {
		t.Fatal(err)
	}
	legacy := rows[len(rows)-1]
	if legacy.ExecutionID != "old-execution" || legacy.ParentToolCallID != "old-parent-call" || legacy.Output != "old-output" || legacy.Error != "old-error" || legacy.Success || legacy.Kind.String() != "unknown" {
		t.Fatalf("legacy capture changed = %+v", legacy)
	}
	if legacy.ParentExecutionID != "" || legacy.TaskID != "" || legacy.Attempt != 0 || legacy.Sequence != 0 || legacy.Dispatched != nil {
		t.Fatalf("legacy defaults = %+v", legacy)
	}
}
