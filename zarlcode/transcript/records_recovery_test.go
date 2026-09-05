package transcript_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
)

func TestFromRecordsRecoversCrashOpenEmptyAssistant(t *testing.T) {
	thread, err := transcript.FromRecords(2, []transcript.Record{
		{Sequence: 1, ID: "e1", Kind: "user_message", Revision: 1, Payload: []byte(`{"text":"hello"}`)},
		{Sequence: 2, ID: "e2", TurnID: "turn", Kind: "assistant_message", Revision: 2, Payload: []byte(`{}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if thread.Revision() != 3 {
		t.Fatalf("revision = %d, want 3", thread.Revision())
	}
	entries := thread.Entries()
	assistant := entries[1]
	if !assistant.Payload.Interrupted || assistant.Payload.Complete || assistant.Payload.Text != "" || assistant.Revision != 3 {
		t.Fatalf("recovered assistant = %#v", assistant)
	}
	if err := thread.Validate(); err != nil {
		t.Fatalf("recovered transcript: %v", err)
	}
}
