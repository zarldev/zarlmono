package transcript_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

func TestControlRecordedOwnsPayloadAndRejectsConflictingDuplicate(t *testing.T) {
	reducer := transcript.NewReducer()
	control := testControl("control-1")
	original := control.Clone()

	change, err := reducer.Apply(transcript.ControlRecorded{TurnID: "turn-1", Control: control})
	if err != nil {
		t.Fatalf("apply control: %v", err)
	}
	if change.PrimaryEntryID != control.ID || !change.Changed() || change.Persistence != transcript.Persistences.PERSISTENCEIMMEDIATE {
		t.Fatalf("unexpected change: %#v", change)
	}

	control.ToolDefinitions[0].Name = "mutated"
	entry := reducer.Thread().Entries()[0]
	if entry.ID != original.ID || entry.Kind != transcript.EntryKinds.ENTRYCONTROLCHANGE || entry.Payload.Control.ToolDefinitions[0].Name != "search" {
		t.Fatalf("stored control was not independently owned: %#v", entry)
	}
	if reducer.Thread().MessageCount() != 0 {
		t.Fatal("control entry counted as a conversation message")
	}

	before := reducer.Thread()
	duplicate, err := reducer.Apply(transcript.ControlRecorded{TurnID: "turn-1", Control: original})
	if err != nil {
		t.Fatalf("apply duplicate control: %v", err)
	}
	if duplicate.Changed() || duplicate.PrimaryEntryID != original.ID || !reflect.DeepEqual(reducer.Thread().Entries(), before.Entries()) {
		t.Fatalf("duplicate was not idempotent: %#v", duplicate)
	}

	conflict := original.Clone()
	conflict.SystemDigest = strings.Repeat("b", 64)
	if _, err := reducer.Apply(transcript.ControlRecorded{TurnID: "turn-1", Control: conflict}); !errors.Is(err, transcript.ErrInvalidEvent) {
		t.Fatalf("conflicting duplicate error = %v, want ErrInvalidEvent", err)
	}
	if reducer.Thread().Revision() != before.Revision() || !reflect.DeepEqual(reducer.Thread().Entries(), before.Entries()) {
		t.Fatal("conflicting duplicate mutated the thread")
	}
}

func TestControlRecordRoundTripExportAndCheckpoint(t *testing.T) {
	reducer := transcript.NewReducer()
	control := testControl("control-roundtrip")
	if _, err := reducer.Apply(transcript.ControlRecorded{TurnID: "turn-2", Control: control}); err != nil {
		t.Fatalf("apply control: %v", err)
	}
	thread := reducer.Thread()
	records, err := thread.RecordsSince(0)
	if err != nil {
		t.Fatalf("records: %v", err)
	}
	restored, err := transcript.FromRecords(thread.Revision(), records)
	if err != nil {
		t.Fatalf("restore records: %v", err)
	}
	if !reflect.DeepEqual(restored.Entries(), thread.Entries()) {
		t.Fatalf("round trip differs\n got: %#v\nwant: %#v", restored.Entries(), thread.Entries())
	}

	markdown := transcript.Markdown(transcript.MarkdownMetadata{Title: "control"}, restored)
	for _, safe := range []string{"Control change", "build", control.SystemDigest, control.SurfaceDigest, "search", control.ToolDefinitions[0].DefinitionHash} {
		if !strings.Contains(markdown, safe) {
			t.Fatalf("export does not contain %q:\n%s", safe, markdown)
		}
	}

	checkpoint, err := restored.CaptureCheckpoint()
	if err != nil {
		t.Fatalf("capture checkpoint: %v", err)
	}
	checkpointThread, err := checkpoint.Restore()
	if err != nil {
		t.Fatalf("restore checkpoint: %v", err)
	}
	entries := checkpointThread.Entries()
	entries[0].Payload.Control.ToolDefinitions[0].Name = "mutated"
	again, err := checkpoint.Restore()
	if err != nil {
		t.Fatalf("restore checkpoint again: %v", err)
	}
	if again.Entries()[0].Payload.Control.ToolDefinitions[0].Name != "search" {
		t.Fatal("checkpoint control payload was not independently owned")
	}
}

func TestControlRecordsRejectMalformedPayloads(t *testing.T) {
	reducer := transcript.NewReducer()
	if _, err := reducer.Apply(transcript.ControlRecorded{TurnID: "turn-3", Control: testControl("control-malformed")}); err != nil {
		t.Fatalf("apply control: %v", err)
	}
	thread := reducer.Thread()
	records, err := thread.RecordsSince(0)
	if err != nil {
		t.Fatalf("records: %v", err)
	}

	tests := map[string]func(map[string]any){
		"unrelated field": func(payload map[string]any) { payload["text"] = "must be rejected" },
		"unknown field":   func(payload map[string]any) { payload["unexpected"] = true },
		"invalid control": func(payload map[string]any) {
			payload["control"].(map[string]any)["surface_digest"] = "short"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			malformed := append([]transcript.Record(nil), records...)
			malformed[0].Payload = bytes.Clone(records[0].Payload)
			var payload map[string]any
			if err := json.Unmarshal(malformed[0].Payload, &payload); err != nil {
				t.Fatalf("decode fixture: %v", err)
			}
			mutate(payload)
			malformed[0].Payload, err = json.Marshal(payload)
			if err != nil {
				t.Fatalf("encode fixture: %v", err)
			}
			if _, err := transcript.FromRecords(thread.Revision(), malformed); err == nil {
				t.Fatal("malformed control record was accepted")
			}
		})
	}
}

func testControl(id string) runner.ReplayControl {
	return runner.ReplayControl{
		Version:       1,
		ID:            id,
		PolicyLabel:   "build",
		SystemDigest:  strings.Repeat("a", 64),
		SurfaceDigest: strings.Repeat("c", 64),
		ToolDefinitions: []runner.ControlToolDefinition{{
			Name: "search", DefinitionHash: strings.Repeat("d", 64),
		}},
	}
}
