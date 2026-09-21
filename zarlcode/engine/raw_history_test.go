package engine_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestToolOutputPreservesRawHistory(t *testing.T) {
	store, err := db.Open(t.Context(), t.TempDir()+"/history.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveSessionDraft(t.Context(), db.SessionRecord{ID: "raw", Workspace: "/workspace", PendingJSON: []byte(`{"text":"draft"}`)}); err != nil {
		t.Fatal(err)
	}
	args := "{ \"auth_token\":\"CANARY-token\", \"env\":{\"KEY\":\"CANARY-env\"} }"
	sink := &engine.ToolOutputSink{Store: store, SessionID: func() string { return "raw" }}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	const uri = "data:image/png;base64,Q0FOQVJZ"
	results := []runner.ToolOutput{
		{ToolCallID: "call", ToolName: "mcp_connect", Args: args, Output: "CANARY-first", Success: false, Error: "CANARY-timeout", Kind: tools.Kinds.TRANSIENT, Parts: []llm.ContentPart{llm.ImagePartFromDataURI(uri, "image/png")}},
		{ToolCallID: "call", ToolName: "mcp_connect", Args: args, Output: "CANARY-second", Success: true, Kind: tools.Kinds.UNKNOWN, Parts: []llm.ContentPart{llm.ImagePartFromDataURI(uri, "image/png")}},
	}
	for _, output := range results {
		if err := sink.Record(ctx, output); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.GetToolOutput(t.Context(), "raw", "call")
	if err != nil {
		t.Fatal(err)
	}
	if got.ArgsJSON != args || got.Output != "CANARY-second" {
		t.Fatalf("latest projection changed: %+v", got)
	}
	history, err := store.ListToolOutputHistory(t.Context(), "raw")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].Output != "CANARY-first" || history[1].Output != "CANARY-second" {
		t.Fatalf("attempts overwritten: %+v", history)
	}
	if history[0].Success || history[0].Error != "CANARY-timeout" || history[0].Kind != tools.Kinds.TRANSIENT {
		t.Fatalf("failed classification changed: %+v", history[0])
	}
	if !history[1].Success || history[1].Error != "" || history[1].Kind != tools.Kinds.UNKNOWN {
		t.Fatalf("success classification changed: %+v", history[1])
	}
	for _, record := range history {
		if record.ArgsJSON != args {
			t.Fatalf("original arguments changed: %q", record.ArgsJSON)
		}
		var parts []llm.ContentPart
		if err := json.Unmarshal([]byte(record.PartsJSON), &parts); err != nil {
			t.Fatal(err)
		}
		if len(parts) != 1 || parts[0].Image.DataURI != uri {
			t.Fatalf("raw attachment lost: %+v", parts)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sink.Record(t.Context(), runner.ToolOutput{ToolCallID: "cannot-save"}); err == nil {
		t.Fatal("capture failure silently dropped")
	}
}

func TestProcessHistoryClassifiesExitWithoutChangingRawOutput(t *testing.T) {
	store, err := db.Open(t.Context(), t.TempDir()+"/process-history.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveSessionDraft(t.Context(), db.SessionRecord{ID: "process", Workspace: "/workspace", PendingJSON: []byte(`{"text":"draft"}`)}); err != nil {
		t.Fatal(err)
	}

	sink := &engine.ToolOutputSink{Store: store, SessionID: func() string { return "process" }}
	effect := tools.NewProcessEffect("CANARY-command", 0)
	effect.Process.Background = true
	effect.Process.ProcessID = "process-1"
	if err := sink.Record(t.Context(), runner.ToolOutput{ToolCallID: "launch", ToolName: "bash", Success: true, Kind: tools.Kinds.UNKNOWN, Effects: []tools.Effect{effect}}); err != nil {
		t.Fatal(err)
	}
	sink.RecordProcess(t.Context(), "process-1", "CANARY-command", 9, []string{"CANARY-stdout"}, []string{"CANARY-stderr"})

	history, err := store.ListToolOutputHistory(t.Context(), "process")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("history length = %d, want 2", len(history))
	}
	got := history[1]
	if got.Output != "CANARY-stdout\nCANARY-stderr" {
		t.Fatalf("raw process output = %q", got.Output)
	}
	if got.Success || got.Error != "process exited with code 9" || got.Kind != tools.Kinds.UNKNOWN {
		t.Fatalf("process classification changed: %+v", got)
	}
}
