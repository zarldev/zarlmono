package transcript_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestToolOutputPreviewUpdatesActiveExactExecution(t *testing.T) {
	t.Parallel()

	reducer := transcript.NewReducer()
	for _, start := range []transcript.ToolStarted{
		{TurnID: "turn", ExecutionID: "first", ToolID: "reused", Name: "bash"},
		{TurnID: "turn", ExecutionID: "second", ToolID: "reused", Name: "bash"},
	} {
		if _, err := reducer.Apply(start); err != nil {
			t.Fatal(err)
		}
	}

	first, err := reducer.Apply(transcript.ToolOutputUpdated{ExecutionID: "first", ToolID: "reused", Output: "one"})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Changed() || first.Persistence != transcript.Persistences.PERSISTENCEDEBOUNCED || len(first.Records) != 1 {
		t.Fatalf("first preview change = %#v", first)
	}
	entries := reducer.Thread().Entries()
	if entries[0].Payload.ToolOutputPreview != "one" || entries[1].Payload.ToolOutputPreview != "" {
		t.Fatalf("exact preview attribution = %#v", entries)
	}

	replacement, err := reducer.Apply(transcript.ToolOutputUpdated{ExecutionID: "first", ToolID: "reused", Output: "replacement"})
	if err != nil {
		t.Fatal(err)
	}
	if !replacement.Changed() || reducer.Thread().Entries()[0].Payload.ToolOutputPreview != "replacement" {
		t.Fatalf("replacement change = %#v entries=%#v", replacement, reducer.Thread().Entries())
	}

	before := reducer.Thread().Revision()
	identical, err := reducer.Apply(transcript.ToolOutputUpdated{ExecutionID: "first", ToolID: "reused", Output: "replacement"})
	if err != nil {
		t.Fatal(err)
	}
	if identical.Changed() || reducer.Thread().Revision() != before || len(identical.Records) != 0 {
		t.Fatalf("identical snapshot changed state: %#v", identical)
	}

	mismatch, err := reducer.Apply(transcript.ToolOutputUpdated{ExecutionID: "first", ToolID: "other", Output: "wrong"})
	if err != nil {
		t.Fatal(err)
	}
	if mismatch.Changed() || reducer.Thread().Entries()[0].Payload.ToolOutputPreview != "replacement" {
		t.Fatalf("mismatched tool ID changed state: %#v", mismatch)
	}

	orphan, err := reducer.Apply(transcript.ToolOutputUpdated{ExecutionID: "unknown", ToolID: "reused", Output: "orphan"})
	if err != nil {
		t.Fatal(err)
	}
	if orphan.Changed() || reducer.Thread().Revision() != before {
		t.Fatalf("orphan preview changed state: %#v", orphan)
	}

	if _, err := reducer.Apply(transcript.ToolFinished{ExecutionID: "first", ToolID: "reused", DurationMS: 1}); err != nil {
		t.Fatal(err)
	}
	terminalRevision := reducer.Thread().Revision()
	late, err := reducer.Apply(transcript.ToolOutputUpdated{ExecutionID: "first", ToolID: "reused", Output: "late"})
	if err != nil {
		t.Fatal(err)
	}
	if late.Changed() || reducer.Thread().Revision() != terminalRevision {
		t.Fatalf("late preview changed terminal tool: %#v", late)
	}
	entries = reducer.Thread().Entries()
	if entries[0].Payload.ToolOutputPreview != "replacement" || entries[0].Payload.ToolState != transcript.ToolSucceeded {
		t.Fatalf("completed tool did not retain preview: %#v", entries[0])
	}
	if len(entries) != 2 || reducer.Thread().MessageCount() != 0 {
		t.Fatalf("preview changed entry/message count: entries=%d messages=%d", len(entries), reducer.Thread().MessageCount())
	}
}

func TestToolOutputPreviewRejectsMalformedEventWithoutMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		event transcript.ToolOutputUpdated
	}{
		{name: "missing execution", event: transcript.ToolOutputUpdated{ToolID: "tool", Output: "output"}},
		{name: "missing tool", event: transcript.ToolOutputUpdated{ExecutionID: "execution", Output: "output"}},
		{name: "invalid UTF-8", event: transcript.ToolOutputUpdated{ExecutionID: "execution", ToolID: "tool", Output: string([]byte{0xff})}},
		{name: "over bound", event: transcript.ToolOutputUpdated{ExecutionID: "execution", ToolID: "tool", Output: strings.Repeat("x", tools.MaxToolOutputPreviewBytes+1)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reducer := transcript.NewReducer()
			if _, err := reducer.Apply(transcript.ToolStarted{TurnID: "turn", ExecutionID: "execution", ToolID: "tool", Name: "bash"}); err != nil {
				t.Fatal(err)
			}
			before := reducer.Thread()
			change, err := reducer.Apply(tt.event)
			if !errors.Is(err, transcript.ErrInvalidEvent) {
				t.Fatalf("error = %v, want ErrInvalidEvent", err)
			}
			if change.Changed() || reducer.Thread().Revision() != before.Revision() || reducer.Thread().Entries()[0].Payload.ToolOutputPreview != "" {
				t.Fatalf("malformed event mutated thread: %#v", change)
			}
		})
	}
}

func TestToolOutputPreviewRecordsCheckpointRecoveryAndMarkdown(t *testing.T) {
	t.Parallel()

	const preview = "line\n```\n"
	reducer := transcript.NewReducer()
	if _, err := reducer.Apply(transcript.ToolStarted{TurnID: "turn", ExecutionID: "execution", ToolID: "tool", Name: "bash"}); err != nil {
		t.Fatal(err)
	}
	if _, err := reducer.Apply(transcript.ToolOutputUpdated{ExecutionID: "execution", ToolID: "tool", Output: preview}); err != nil {
		t.Fatal(err)
	}
	if _, err := reducer.Apply(transcript.ToolFinished{ExecutionID: "execution", ToolID: "tool", DurationMS: 1}); err != nil {
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
	if got := restored.Entries()[0].Payload.ToolOutputPreview; got != preview {
		t.Fatalf("round-trip preview = %q, want %q", got, preview)
	}
	checkpoint, err := restored.CaptureCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	checkpointThread, err := checkpoint.Restore()
	if err != nil {
		t.Fatal(err)
	}
	if got := checkpointThread.Entries()[0].Payload.ToolOutputPreview; got != preview {
		t.Fatalf("checkpoint preview = %q, want %q", got, preview)
	}

	markdown := transcript.Markdown(transcript.MarkdownMetadata{Title: "session"}, restored)
	want := "Output preview (bounded):\n\n````\n" + preview + "````"
	if !strings.Contains(markdown, want) {
		t.Fatalf("Markdown did not use labelled safe preview fence:\n%s", markdown)
	}

	old := []transcript.Record{{
		Sequence: 1, ID: "e1", TurnID: "turn", Kind: "tool_call", Revision: 1,
		Payload: []byte(`{"tool_id":"tool","tool_name":"bash","tool_state":"succeeded"}`),
	}}
	legacy, err := transcript.FromRecords(1, old)
	if err != nil {
		t.Fatal(err)
	}
	if got := legacy.Entries()[0].Payload.ToolOutputPreview; got != "" {
		t.Fatalf("legacy preview = %q, want empty", got)
	}
}

func TestToolOutputPreviewRetainedOnInterruption(t *testing.T) {
	t.Parallel()

	reducer := transcript.NewReducer()
	if _, err := reducer.Apply(transcript.ToolStarted{TurnID: "turn", ExecutionID: "execution", ToolID: "tool", Name: "bash"}); err != nil {
		t.Fatal(err)
	}
	if _, err := reducer.Apply(transcript.ToolOutputUpdated{ExecutionID: "execution", ToolID: "tool", Output: "partial"}); err != nil {
		t.Fatal(err)
	}
	recovered, changed := reducer.Thread().RecoverInterrupted()
	if !changed {
		t.Fatal("RecoverInterrupted reported no change")
	}
	entry := recovered.Entries()[0]
	if entry.Payload.ToolState != transcript.ToolInterrupted || entry.Payload.ToolOutputPreview != "partial" {
		t.Fatalf("interrupted tool = %#v", entry)
	}
}

func TestToolOutputPreviewStoredShapeValidation(t *testing.T) {
	t.Parallel()

	wrongKind := []transcript.Record{{
		Sequence: 1, ID: "e1", Kind: "user_message", Revision: 1,
		Payload: []byte(`{"text":"prompt","tool_output_preview":"output"}`),
	}}
	if _, err := transcript.FromRecords(1, wrongKind); err == nil || !strings.Contains(err.Error(), "tool output preview belongs only to tool entries") {
		t.Fatalf("wrong-kind error = %v", err)
	}

	payload, err := json.Marshal(transcript.Payload{
		ToolID: "tool", ToolName: "bash", ToolState: transcript.ToolRunning,
		ToolOutputPreview: strings.Repeat("x", tools.MaxToolOutputPreviewBytes+1),
	})
	if err != nil {
		t.Fatal(err)
	}
	overBound := []transcript.Record{{Sequence: 1, ID: "e1", Kind: "tool_call", Revision: 1, Payload: payload}}
	if _, err := transcript.FromRecords(1, overBound); err == nil || !strings.Contains(err.Error(), "tool output preview exceeds") {
		t.Fatalf("over-bound stored error = %v", err)
	}
}
