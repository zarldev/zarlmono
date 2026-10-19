package transcript_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
)

func TestToolStartedPersistsPresentationInputBeforeTerminal(t *testing.T) {
	t.Parallel()

	const (
		providerArgument = `provider-history-argument`
		input            = "{\n  \"path\": \"already-redacted\",\n  \"source\": \"```\"\n}"
	)
	reducer := transcript.NewReducer()
	start, err := reducer.Apply(transcript.ToolStarted{
		TurnID: "turn", ExecutionID: "execution", ToolID: "tool", Name: "edit",
		Argument: providerArgument, Input: input,
	})
	if err != nil {
		t.Fatal(err)
	}
	if start.AfterRevision-start.BeforeRevision != 1 || len(start.Records) != 1 {
		t.Fatalf("start change = %#v, want one semantic revision and record", start)
	}
	entries := reducer.Thread().Entries()
	if len(entries) != 1 || entries[0].Payload.ToolState != transcript.ToolRunning {
		t.Fatalf("running entries = %#v", entries)
	}
	if entries[0].Payload.ToolInput != input || entries[0].Payload.Argument != providerArgument {
		t.Fatalf("tool payload = %#v", entries[0].Payload)
	}
	if !json.Valid(start.Records[0].Payload) {
		t.Fatalf("start payload is not JSON: %s", start.Records[0].Payload)
	}

	markdown := transcript.Markdown(transcript.MarkdownMetadata{Title: "session"}, reducer.Thread())
	if !strings.Contains(markdown, "Arguments:\n\n````json\n"+input+"\n````") {
		t.Fatalf("Markdown did not use a safe input fence:\n%s", markdown)
	}

	if _, err := reducer.Apply(transcript.ToolFinished{ExecutionID: "execution", ToolID: "tool", Effect: "updated", DurationMS: 1}); err != nil {
		t.Fatal(err)
	}
	thread := reducer.Thread()
	records, err := thread.RecordsSince(0)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := transcript.FromRecords(thread.Revision(), records)
	if err != nil {
		t.Fatal(err)
	}
	got := restored.Entries()[0].Payload
	if got.ToolInput != input || got.Argument != providerArgument || got.ToolState != transcript.ToolSucceeded {
		t.Fatalf("restored tool payload = %#v", got)
	}
	checkpoint, err := restored.CaptureCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	checkpointThread, err := checkpoint.Restore()
	if err != nil {
		t.Fatal(err)
	}
	if got := checkpointThread.Entries()[0].Payload.ToolInput; got != input {
		t.Fatalf("checkpoint tool input = %q, want %q", got, input)
	}
}

func TestToolInputUsesExactExecutionIdentityWithRepeatedToolIDs(t *testing.T) {
	t.Parallel()

	reducer := transcript.NewReducer()
	for _, event := range []transcript.ToolStarted{
		{TurnID: "turn", ExecutionID: "first", ToolID: "reused", Name: "read", Input: `{"path":"first"}`},
		{TurnID: "turn", ExecutionID: "second", ToolID: "reused", Name: "read", Input: `{"path":"second"}`},
	} {
		change, err := reducer.Apply(event)
		if err != nil {
			t.Fatal(err)
		}
		if change.AfterRevision-change.BeforeRevision != 1 {
			t.Fatalf("start change = %#v, want one revision", change)
		}
	}
	if _, err := reducer.Apply(transcript.ToolFinished{ExecutionID: "second", ToolID: "reused", DurationMS: 2}); err != nil {
		t.Fatal(err)
	}
	entries := reducer.Thread().Entries()
	if entries[0].Payload.ToolInput != `{"path":"first"}` || entries[0].Payload.ToolState != transcript.ToolRunning {
		t.Fatalf("first occurrence = %#v", entries[0])
	}
	if entries[1].Payload.ToolInput != `{"path":"second"}` || entries[1].Payload.ToolState != transcript.ToolSucceeded {
		t.Fatalf("second occurrence = %#v", entries[1])
	}
}

func TestToolInputRecordCompatibilityAndKindOwnership(t *testing.T) {
	t.Parallel()

	old := []transcript.Record{{
		Sequence: 1, ID: "e1", TurnID: "turn", Kind: "tool_call", Revision: 1,
		Payload: []byte(`{"tool_id":"tool","tool_name":"read","tool_state":"running"}`),
	}}
	thread, err := transcript.FromRecords(1, old)
	if err != nil {
		t.Fatal(err)
	}
	if got := thread.Entries()[0].Payload.ToolInput; got != "" {
		t.Fatalf("legacy tool input = %q, want empty", got)
	}

	wrongKind := []transcript.Record{{
		Sequence: 1, ID: "e1", Kind: "user_message", Revision: 1,
		Payload: []byte(`{"text":"prompt","tool_input":"already-redacted-canary"}`),
	}}
	if _, err := transcript.FromRecords(1, wrongKind); err == nil || !strings.Contains(err.Error(), "tool input belongs only to tool entries") {
		t.Fatalf("wrong-kind error = %v", err)
	}
}
