package transcript_test

import (
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
)

func TestInputEventsRetainTypedFactsThroughRecordsAndCheckpoint(t *testing.T) {
	r := transcript.NewReducer()
	for _, event := range []any{
		transcript.InputWaitChanged{TurnID: "parent", Text: "Wait started", Waiting: true},
		transcript.InputAdmitted{TurnID: "parent", Text: "Evidence admitted", Admission: transcript.InputAdmission{Namespace: "completion", ID: "child"}},
		transcript.InputAdmitted{TurnID: "parent", Text: "Explicit result admitted", Admission: transcript.InputAdmission{Namespace: "completion", ID: "other", Explicit: true}},
		transcript.InputWaitChanged{TurnID: "parent", Text: "Wait ended"},
	} {
		change, err := r.Apply(event)
		if err != nil {
			t.Fatal(err)
		}
		if change.Persistence != transcript.Persistences.PERSISTENCEIMMEDIATE {
			t.Fatalf("persistence = %v", change.Persistence)
		}
	}
	thread := r.Thread()
	records, err := thread.RecordsSince(0)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := transcript.FromRecords(thread.Revision(), records)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(thread.Entries(), restored.Entries()) {
		t.Fatalf("record replay changed typed input facts: %#v", restored.Entries())
	}
	checkpoint, err := restored.CaptureCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := checkpoint.Restore()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(thread.Entries(), loaded.Entries()) {
		t.Fatal("checkpoint lost admission or wait metadata")
	}
	entries := loaded.Entries()
	if entries[0].Kind != transcript.EntryKinds.ENTRYINPUTWAIT || !entries[0].Payload.InputWaiting || entries[3].Payload.InputWaiting {
		t.Fatalf("wait facts = %#v", entries)
	}
	if entries[1].Kind != transcript.EntryKinds.ENTRYINPUTADMISSION || entries[1].Payload.InputAdmission.Explicit || !entries[2].Payload.InputAdmission.Explicit {
		t.Fatalf("admission facts = %#v", entries)
	}
	if loaded.MessageCount() != 0 {
		t.Fatal("host input facts became human/assistant messages")
	}
}

func TestInputEventsRejectMissingIdentity(t *testing.T) {
	for _, event := range []any{
		transcript.InputAdmitted{TurnID: "parent", Text: "missing receipt"},
		transcript.InputWaitChanged{Text: "missing parent", Waiting: true},
	} {
		r := transcript.NewReducer()
		if _, err := r.Apply(event); err == nil {
			t.Fatalf("accepted invalid event %#v", event)
		}
		if !r.Thread().IsEmpty() {
			t.Fatal("invalid event changed transcript")
		}
	}
}
