package transcript_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
)

func TestAssistantSegmentBoundariesPreserveCanonicalText(t *testing.T) {
	t.Parallel()

	reducer := transcript.NewReducer()
	if _, err := reducer.Apply(transcript.TurnStarted{TurnID: "turn"}); err != nil {
		t.Fatal(err)
	}
	before := reducer.Thread().Revision()
	empty, err := reducer.Apply(transcript.AssistantSegmentFinished{TurnID: "turn"})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Changed() || reducer.Thread().Revision() != before {
		t.Fatalf("empty boundary changed transcript: %#v", empty)
	}

	const first = "**read-only.**"
	const second = "**All four**"
	if _, err := reducer.Apply(transcript.AssistantDelta{TurnID: "turn", Delta: first}); err != nil {
		t.Fatal(err)
	}
	boundary, err := reducer.Apply(transcript.AssistantSegmentFinished{TurnID: "turn"})
	if err != nil {
		t.Fatal(err)
	}
	if !boundary.Changed() || boundary.Persistence != transcript.Persistences.PERSISTENCEIMMEDIATE || len(boundary.Records) != 1 {
		t.Fatalf("boundary change = %#v", boundary)
	}
	if _, err := reducer.Apply(transcript.AssistantDelta{TurnID: "turn", Delta: second}); err != nil {
		t.Fatal(err)
	}
	if _, err := reducer.Apply(transcript.AssistantSegmentFinished{TurnID: "turn"}); err != nil {
		t.Fatal(err)
	}
	duplicate, err := reducer.Apply(transcript.AssistantSegmentFinished{TurnID: "turn"})
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.Changed() {
		t.Fatalf("duplicate boundary changed transcript: %#v", duplicate)
	}

	entries := reducer.Thread().Entries()
	if len(entries) != 1 {
		t.Fatalf("entries = %#v", entries)
	}
	payload := entries[0].Payload
	if payload.Text != first+second {
		t.Fatalf("text = %q, want exact concatenated bytes %q", payload.Text, first+second)
	}
	if want := []int{len(first), len(first + second)}; !reflect.DeepEqual(payload.TextBreaks, want) {
		t.Fatalf("text breaks = %#v, want %#v", payload.TextBreaks, want)
	}
	if want := []string{first, second}; !reflect.DeepEqual(payload.TextSegments(), want) {
		t.Fatalf("segments = %#v, want %#v", payload.TextSegments(), want)
	}
}

func TestAssistantTextBreakSnapshotsAndRecordsAreIndependent(t *testing.T) {
	t.Parallel()

	builder := transcript.NewBuilder()
	builder.AppendAssistant("turn", "", "one")
	builder.FinishAssistantSegment("turn")
	builder.AppendAssistant("turn", "", "two")
	builder.FinishTurn("turn")

	entries := builder.Thread().Entries()
	entries[0].Payload.TextBreaks[0] = 1
	if got := builder.Thread().Entries()[0].Payload.TextBreaks[0]; got != len("one") {
		t.Fatalf("canonical text break aliased snapshot: %d", got)
	}

	records, err := builder.Thread().RecordsSince(0)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(records[0].Payload); got != `{"text":"onetwo","text_breaks":[3],"complete":true}` {
		t.Fatalf("payload = %s", got)
	}
	restored, err := transcript.FromRecords(builder.Thread().Revision(), records)
	if err != nil {
		t.Fatal(err)
	}
	if got := restored.Entries()[0].Payload.TextSegments(); !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Fatalf("restored segments = %#v", got)
	}

	old := []transcript.Record{{
		Sequence: 1, ID: "e1", TurnID: "old", Kind: "assistant_message", Revision: 1,
		Payload: []byte(`{"text":"legacy","complete":true}`),
	}}
	legacy, err := transcript.FromRecords(1, old)
	if err != nil {
		t.Fatal(err)
	}
	if got := legacy.Entries()[0].Payload.TextSegments(); !reflect.DeepEqual(got, []string{"legacy"}) {
		t.Fatalf("legacy segments = %#v", got)
	}
}

func TestStoredTextBreakValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		kind    string
		payload string
	}{
		{name: "zero", kind: "assistant_message", payload: `{"text":"abc","text_breaks":[0],"complete":true}`},
		{name: "duplicate", kind: "assistant_message", payload: `{"text":"abc","text_breaks":[1,1],"complete":true}`},
		{name: "descending", kind: "assistant_message", payload: `{"text":"abc","text_breaks":[2,1],"complete":true}`},
		{name: "out of range", kind: "assistant_message", payload: `{"text":"abc","text_breaks":[4],"complete":true}`},
		{name: "middle of UTF-8", kind: "assistant_message", payload: `{"text":"aéz","text_breaks":[2],"complete":true}`},
		{name: "irrelevant kind", kind: "reasoning", payload: `{"text":"abc","text_breaks":[1],"complete":true}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			records := []transcript.Record{{Sequence: 1, ID: "e1", TurnID: "turn", Kind: tt.kind, Revision: 1, Payload: []byte(tt.payload)}}
			if _, err := transcript.FromRecords(1, records); err == nil {
				t.Fatal("FromRecords accepted invalid text breaks")
			}
			if _, err := transcript.CheckpointFromRecords(1, records); err == nil {
				t.Fatal("CheckpointFromRecords accepted invalid text breaks")
			}
		})
	}
}

func TestMarkdownSeparatesAssistantSourceSegmentsWithoutChangingRawText(t *testing.T) {
	t.Parallel()

	builder := transcript.NewBuilder()
	builder.AppendAssistant("turn", "", "**first**")
	builder.FinishAssistantSegment("turn")
	builder.AppendAssistant("turn", "", "**second**")
	builder.FinishTurn("turn")
	before := builder.Thread().Entries()[0].Payload.Text

	exported := transcript.Markdown(transcript.MarkdownMetadata{Title: "segments"}, builder.Thread())
	if !strings.Contains(exported, "### Assistant\n\n**first**\n\n**second**\n") {
		t.Fatalf("Markdown did not separate raw assistant sections:\n%s", exported)
	}
	if strings.Contains(exported, "**first****second**") {
		t.Fatalf("Markdown joined independent assistant sources:\n%s", exported)
	}
	if after := builder.Thread().Entries()[0].Payload.Text; after != before || after != "**first****second**" {
		t.Fatalf("Markdown changed canonical text: before=%q after=%q", before, after)
	}
}
