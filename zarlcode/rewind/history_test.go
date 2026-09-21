package rewind_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/draft"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
)

func persistHistoryCheckpoint(t *testing.T, input rewind.CaptureInput, messages []llm.Message) (*db.Store, rewind.Checkpoint) {
	t.Helper()
	store, err := db.Open(t.Context(), t.TempDir()+"/history.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveActiveSession(t.Context(), db.SessionRecord{ID: input.SessionID, Workspace: input.Workspace}); err != nil {
		t.Fatal(err)
	}
	initial, err := store.CaptureSessionHistory(t.Context(), input.SessionID, nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSessionCheckpoint(t.Context(), db.SessionCheckpoint{SessionID: input.SessionID, ID: "initial", SourceSessionID: input.SessionID, Workspace: input.Workspace, BoundaryID: "initial-prompt", FormatVersion: 2, Payload: []byte(`{}`), History: initial}); err != nil {
		t.Fatal(err)
	}
	var replay [][]byte
	for _, message := range messages {
		data, err := runner.MarshalReplayMessage(runner.ReplayMessage{Message: message})
		if err != nil {
			t.Fatal(err)
		}
		replay = append(replay, data)
	}
	if err := store.AppendSessionReplay(t.Context(), input.SessionID, replay); err != nil {
		t.Fatal(err)
	}
	records, err := input.Transcript.Records()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		entries := make([]db.TranscriptEntry, len(records))
		for i, r := range records {
			entries[i] = db.TranscriptEntry{Sequence: r.Sequence, EntryID: r.ID, ParentID: r.ParentID, TurnID: r.TurnID, Kind: r.Kind, Revision: r.Revision, PayloadJSON: r.Payload}
		}
		if err := store.UpdateActiveTranscript(t.Context(), db.TranscriptUpdate{SessionID: input.SessionID, Workspace: input.Workspace, Revision: input.Transcript.Revision(), Entries: entries}); err != nil {
			t.Fatal(err)
		}
	}
	checkpoint, err := rewind.CaptureHistory(t.Context(), store, input)
	if err != nil {
		t.Fatal(err)
	}
	record, err := checkpoint.Record()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSessionCheckpoint(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	record, err = store.GetSessionCheckpoint(t.Context(), input.SessionID, input.ID)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err = rewind.Load(t.Context(), store, record)
	if err != nil {
		t.Fatal(err)
	}
	return store, checkpoint
}

func TestHistoryCheckpointRestoresNonUTF8Replay(t *testing.T) {
	input := captureInput(t)
	input.Context[1].Content = "raw\xff\x00"
	want := llm.CloneMessages(input.Context)
	_, checkpoint := persistHistoryCheckpoint(t, input, input.Context)
	snapshot, err := checkpoint.Snapshot()
	if err != nil || !reflect.DeepEqual(snapshot.Context, want) {
		t.Fatalf("replay bytes changed: %v", err)
	}
}

func TestHistoryCheckpointAttachmentBranchAndSharedReplay(t *testing.T) {
	input := captureInput(t)
	parts := []llm.ContentPart{llm.TextPart("  file bytes\n"), llm.ImagePartFromDataURI("data:image/png;base64,eA==", "image/png")}
	input.Boundary.HasAttachments, input.Boundary.Attachments = true, parts
	input.Context[0].Content = ""
	input.Context[0].Parts = llm.CloneContentParts(parts)
	want := llm.CloneMessages(input.Context)
	store, checkpoint := persistHistoryCheckpoint(t, input, input.Context)
	parts[1].Image.DataURI = "mutated"
	snapshot, err := checkpoint.Snapshot()
	if err != nil || !reflect.DeepEqual(snapshot.Context, want) || snapshot.Boundary.Attachments[1].Image.DataURI != "data:image/png;base64,eA==" {
		t.Fatalf("attachment/native fidelity: %v", err)
	}
	branch, err := checkpoint.PrepareBranch("child", "child", input.Transcript.Revision(), input.Target)
	if err != nil {
		t.Fatal(err)
	}
	attachments, err := draft.DecodeAttachments(branch.Child.PendingJSON)
	if err != nil || !reflect.DeepEqual(attachments, want[0].Parts) {
		t.Fatalf("branch draft lost bytes: %v", err)
	}
	if err := store.CreateCheckpointBranch(t.Context(), branch); err != nil {
		t.Fatal(err)
	}
	source, err := store.CaptureSessionHistory(t.Context(), input.SessionID, nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.CaptureSessionHistory(t.Context(), "child", nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_, original, _, err := store.ReadCheckpointHistory(t.Context(), source)
	if err != nil || len(original) != len(want) {
		t.Fatalf("notice mutated source: %v", err)
	}
	_, continued, _, err := store.ReadCheckpointHistory(t.Context(), child)
	if err != nil || len(continued) != len(want)+1 {
		t.Fatalf("notice suffix missing: %v", err)
	}
	notice, err := runner.UnmarshalReplayMessage(continued[len(want)])
	if err != nil || notice.Message.Content != rewind.FilesUnchangedNotice {
		t.Fatalf("notice occurrence: %v", err)
	}
	if err := store.DeleteSession(t.Context(), input.SessionID); err != nil {
		t.Fatal(err)
	}
	copied, err := store.GetSessionCheckpoint(t.Context(), "child", input.ID)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := rewind.Load(t.Context(), store, copied)
	if err != nil {
		t.Fatal(err)
	}
	again, err := loaded.Snapshot()
	if err != nil || !reflect.DeepEqual(again.Context, want) {
		t.Fatalf("source deletion lost shared bytes: %v", err)
	}
}

func TestHistoryCheckpointSizeIndependentOfConversation(t *testing.T) {
	input := captureInput(t)
	huge := strings.Repeat("x", db.CheckpointPayloadLimit+1)
	builder := transcript.NewBuilder()
	builder.AddUser("prompt")
	builder.AppendAssistant("settled", "", huge)
	builder.FinishTurn("settled")
	var err error
	input.Transcript, err = builder.Thread().CaptureCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	input.Boundary.EventWatermark = input.Transcript.Revision()
	replay := []llm.Message{{Role: llm.RoleUser, Content: "prompt"}, {Role: llm.RoleAssistant, Content: huge}}
	input.Context = []llm.Message{{Role: llm.RoleUser, Content: "compacted working context"}}
	_, checkpoint := persistHistoryCheckpoint(t, input, replay)
	record, err := checkpoint.Record()
	if err != nil || len(record.Payload) > 256 {
		t.Fatalf("checkpoint scales with conversation: %d, %v", len(record.Payload), err)
	}
	snapshot, err := checkpoint.Snapshot()
	if err != nil || !reflect.DeepEqual(snapshot.Context, replay) {
		t.Fatalf("replay used compacted context: %v", err)
	}
}

func TestHistoryMetadataOnlyAttachmentsRemainUnavailable(t *testing.T) {
	input := captureInput(t)
	input.Boundary.HasAttachments = true
	if _, err := rewind.Capture(input); !errors.Is(err, rewind.ErrAttachments) {
		t.Fatalf("fabricated missing bytes: %v", err)
	}
}
