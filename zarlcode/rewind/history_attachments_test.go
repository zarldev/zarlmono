package rewind_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/draft"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestHistoryLoadRejectsCorruptAttachmentBoundary(t *testing.T) {
	input := captureInput(t)
	store, checkpoint := persistHistoryCheckpoint(t, input, input.Context)
	record, err := checkpoint.Record()
	if err != nil {
		t.Fatal(err)
	}
	entries, _, metadata, err := store.ReadCheckpointHistory(t.Context(), record.History)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		has   bool
		parts []llm.ContentPart
	}{
		{"missing bytes", true, nil},
		{"unmarked bytes", false, []llm.ContentPart{llm.TextPart("bytes")}},
		{"empty text", true, []llm.ContentPart{llm.TextPart("")}},
		{"invalid image", true, []llm.ContentPart{llm.ImagePartFromDataURI("data:image/png;base64,???", "image/png")}},
		{"mixed discriminator", true, []llm.ContentPart{{Type: llm.ContentTypeText, Text: "text", Image: llm.ImagePartFromDataURI("data:image/png;base64,eA==", "image/png").Image}}},
		{"too many", true, make([]llm.ContentPart, draft.MaxAttachments+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var state map[string]json.RawMessage
			if err := json.Unmarshal(metadata, &state); err != nil {
				t.Fatal(err)
			}
			var boundary rewind.Boundary
			if err := json.Unmarshal(state["boundary"], &boundary); err != nil {
				t.Fatal(err)
			}
			boundary.HasAttachments, boundary.Attachments = tc.has, tc.parts
			state["boundary"], err = json.Marshal(boundary)
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			changed := record
			changed.History, err = store.CaptureSessionHistory(t.Context(), input.SessionID, entries, data)
			if err != nil {
				t.Fatal(err)
			}
			changed.Payload, err = json.Marshal(map[string]string{"state_id": changed.History.StateID})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := rewind.Load(t.Context(), store, changed); !errors.Is(err, rewind.ErrInvalid) {
				t.Fatalf("accepted corrupt boundary: %v", err)
			}
		})
	}
}
