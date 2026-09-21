package rewind_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/draft"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

func captureInput(t *testing.T) rewind.CaptureInput {
	t.Helper()
	builder := transcript.NewBuilder()
	builder.AddUser("earlier prompt")
	builder.AppendAssistant("settled", "", "earlier answer")
	builder.FinishTurn("settled")
	canonical, err := builder.Thread().CaptureCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	return rewind.CaptureInput{
		ID: "checkpoint", SessionID: "source", Workspace: t.TempDir(),
		Boundary:    rewind.Boundary{PromptID: "next", PromptText: "edit me", SettledTurnID: "settled", EventWatermark: canonical.Revision()},
		Transcript:  canonical,
		Context:     []llm.Message{{Role: llm.RoleUser, Content: "earlier prompt"}, {Role: llm.RoleAssistant, Content: "earlier answer", ContentOutputIndex: llm.OutputPosition(1), ContinuationItems: []llm.ContinuationItem{{Provider: "openai-codex", Format: "responses.reasoning.v1", Kind: "reasoning", OutputIndex: llm.OutputPosition(0), Data: []byte("{ \"type\": \"reasoning\", \"id\": \"item\", \"encrypted_content\": \"private-canary\", \"future_sdk_field\": true }\n")}}}},
		Target:      rewind.Target{Provider: "openai-codex", Model: "saved-model", Window: 128000, Reserve: 4096},
		Plan:        code.Plan{Steps: []code.PlanStep{{Text: "historical intent", Status: code.StepStatuses.COMPLETED}}},
		ToolCallIDs: []string{"historical-output"},
	}
}

func captureRecord(t *testing.T, input rewind.CaptureInput) db.SessionCheckpoint {
	t.Helper()
	checkpoint, err := rewind.Capture(input)
	if err != nil {
		t.Fatal(err)
	}
	record, err := checkpoint.Record()
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestCheckpointRoundTripAndOwnership(t *testing.T) {
	t.Parallel()
	input := captureInput(t)
	want := llm.CloneMessages(input.Context)
	record := captureRecord(t, input)
	decoded, err := rewind.Decode(record)
	if err != nil {
		t.Fatal(err)
	}
	input.Context[1].ContinuationItems[0].Data[0] = '!'
	input.Plan.Steps[0].Text = "future"
	input.ToolCallIDs[0] = "future"
	record.Payload[0] = '!'
	snapshot, err := decoded.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.Context, want) {
		t.Fatal("context changed or native bytes were canonicalized")
	}
	if snapshot.Plan.Steps[0].Text != "historical intent" || snapshot.ToolCallIDs[0] != "historical-output" {
		t.Fatal("capture aliases input")
	}
	snapshot.Context[1].ContinuationItems[0].Data[0] = '!'
	snapshot.Plan.Steps[0].Text = "caller mutation"
	again, err := decoded.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again.Context, want) || again.Plan.Steps[0].Text != "historical intent" {
		t.Fatal("snapshot aliases immutable state")
	}
	records, err := again.Transcript.Records()
	if err != nil {
		t.Fatal(err)
	}
	original, err := input.Transcript.Records()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(records, original) {
		t.Fatal("canonical record bytes changed")
	}
}

func TestCheckpointEmptyAndUnavailable(t *testing.T) {
	t.Parallel()
	input := captureInput(t)
	var err error
	input.Transcript, err = transcript.NewBuilder().Thread().CaptureCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	input.Context = nil
	input.Boundary.SettledTurnID, input.Boundary.EventWatermark = "", 0
	record := captureRecord(t, input)
	decoded, err := rewind.Decode(record)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := decoded.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Context) != 0 || snapshot.Transcript.Revision() != 0 {
		t.Fatal("initial checkpoint not empty")
	}
	var absent rewind.Checkpoint
	if _, err := absent.Record(); !errors.Is(err, rewind.ErrUnavailable) {
		t.Fatalf("Record: %v", err)
	}
	if _, err := absent.Snapshot(); !errors.Is(err, rewind.ErrUnavailable) {
		t.Fatalf("Snapshot: %v", err)
	}
	if _, err := absent.PrepareBranch("child", "label", 0, input.Target); !errors.Is(err, rewind.ErrUnavailable) {
		t.Fatalf("PrepareBranch: %v", err)
	}
}

func TestCheckpointRejectsCorruptEnvelope(t *testing.T) {
	t.Parallel()
	record := captureRecord(t, captureInput(t))
	for _, tc := range []struct {
		name   string
		change func(*db.SessionCheckpoint)
	}{
		{"unknown field", func(r *db.SessionCheckpoint) { r.Payload = append([]byte(`{"private-canary":1,`), r.Payload[1:]...) }},
		{"trailing JSON", func(r *db.SessionCheckpoint) { r.Payload = append(r.Payload, []byte(` {}`)...) }},
		{"version", func(r *db.SessionCheckpoint) {
			r.Payload = bytes.Replace(r.Payload, []byte(`"version":1`), []byte(`"version":99`), 1)
		}},
		{"null context", func(r *db.SessionCheckpoint) { mutatePayload(t, r, func(p map[string]any) { p["context"] = nil }) }},
		{"missing records", func(r *db.SessionCheckpoint) { mutatePayload(t, r, func(p map[string]any) { delete(p, "records") }) }},
		{"canonical corruption", func(r *db.SessionCheckpoint) {
			mutatePayload(t, r, func(p map[string]any) { p["records"].([]any)[0].(map[string]any)["payload"] = "e30=" })
		}},
		{"nested secret field", func(r *db.SessionCheckpoint) {
			mutatePayload(t, r, func(p map[string]any) { p["target"].(map[string]any)["api_key"] = "private-canary" })
		}},
		{"source mismatch", func(r *db.SessionCheckpoint) { r.SourceSessionID = "private-canary" }},
		{"boundary mismatch", func(r *db.SessionCheckpoint) { r.BoundaryID = "private-canary" }},
		{"workspace mismatch", func(r *db.SessionCheckpoint) { r.Workspace = "private-canary" }},
		{"format mismatch", func(r *db.SessionCheckpoint) { r.FormatVersion++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			changed := record
			changed.Payload = bytes.Clone(record.Payload)
			tc.change(&changed)
			_, err := rewind.Decode(changed)
			if !errors.Is(err, rewind.ErrInvalid) {
				t.Fatalf("Decode: %v", err)
			}
			if strings.Contains(err.Error(), "private-canary") {
				t.Fatal("private data leaked")
			}
		})
	}
}

func mutatePayload(t *testing.T, record *db.SessionCheckpoint, mutate func(map[string]any)) {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	mutate(payload)
	var err error
	record.Payload, err = json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPrepareBranchExactPrefixAndDraft(t *testing.T) {
	t.Parallel()
	input := captureInput(t)
	record := captureRecord(t, input)
	// Decode requires an integrity-checked DB record; DB integration tests exercise
	// the real checksum. This value only exercises pure branch preparation.
	record.Checksum = "verified-by-store"
	saved, err := rewind.Decode(record)
	if err != nil {
		t.Fatal(err)
	}
	branch, err := saved.PrepareBranch("child", "branch", input.Transcript.Revision()+9, input.Target)
	if err != nil {
		t.Fatal(err)
	}
	if branch.SourceSessionID != input.SessionID || branch.Child.ID != "child" || branch.CheckpointChecksum != record.Checksum {
		t.Fatal("incorrect provenance")
	}
	var context []llm.Message
	if err := json.Unmarshal(branch.Child.ContextJSON, &context); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(context, input.Context) {
		t.Fatal("branch context changed")
	}
	pending, err := draft.Decode(branch.Child.PendingJSON)
	if err != nil {
		t.Fatal(err)
	}
	if pending != input.Boundary.PromptText {
		t.Fatalf("draft=%q", pending)
	}
	records, err := input.Transcript.Records()
	if err != nil {
		t.Fatal(err)
	}
	if len(branch.Entries) != len(records) {
		t.Fatal("prefix length changed")
	}
	for i, record := range records {
		if !bytes.Equal(branch.Entries[i].PayloadJSON, record.Payload) {
			t.Fatal("prefix bytes changed")
		}
	}
	target := input.Target
	target.Model = "other-model"
	if _, err := saved.PrepareBranch("child", "branch", 99, target); !errors.Is(err, rewind.ErrTarget) {
		t.Fatalf("target: %v", err)
	}
	input.Boundary.HasAttachments = true
	if _, err := rewind.Capture(input); !errors.Is(err, rewind.ErrAttachments) {
		t.Fatalf("attachments: %v", err)
	}
}

func TestCheckpointPayloadLimit(t *testing.T) {
	t.Parallel()
	input := captureInput(t)
	input.Context[0].Content = strings.Repeat("x", db.CheckpointPayloadLimit)
	if _, err := rewind.Capture(input); !errors.Is(err, db.ErrCheckpointQuota) {
		t.Fatalf("Capture: %v", err)
	}
}
