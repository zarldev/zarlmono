package transcript_test

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestCheckpointInitialBoundaryAndUnavailableZero(t *testing.T) {
	t.Parallel()

	var unavailable transcript.Checkpoint
	if _, err := unavailable.Restore(); !errors.Is(err, transcript.ErrCheckpointUnavailable) {
		t.Fatalf("zero Restore error = %v", err)
	}
	if _, err := unavailable.Records(); !errors.Is(err, transcript.ErrCheckpointUnavailable) {
		t.Fatalf("zero Records error = %v", err)
	}

	checkpoint, err := transcript.NewBuilder().Thread().CaptureCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	records := checkpointRecords(t, checkpoint)
	if len(records) != 0 || checkpoint.Revision() != 0 {
		t.Fatalf("initial records = %d, revision = %d", len(records), checkpoint.Revision())
	}
	loaded, err := transcript.CheckpointFromRecords(checkpoint.Revision(), records)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := loaded.Restore()
	if err != nil {
		t.Fatal(err)
	}
	if !restored.IsEmpty() || restored.Revision() != 0 {
		t.Fatalf("initial restored thread = %#v", restored)
	}
}

func TestCheckpointOwnsHistoricalRecords(t *testing.T) {
	t.Parallel()

	builder := settledCheckpointBuilder()
	before := builder.Thread()
	want := mustRecords(t, before)
	checkpoint, err := before.CaptureCheckpoint()
	if err != nil {
		t.Fatal(err)
	}

	// Plan and queued records can change in place after capture. A checkpoint
	// holding only IDs would restore these future payloads into the earlier turn.
	builder.SetPlan("future", code.Plan{Steps: []code.PlanStep{{Text: "future plan", Status: code.StepStatuses.COMPLETED}}})
	for _, entry := range before.Entries() {
		if entry.Kind == transcript.EntryKinds.ENTRYQUEUEDUSER {
			builder.InjectQueuedUser(entry.ID, "future queued text")
		}
	}
	builder.AddUser("protected prompt")
	builder.AppendAssistant("future", "", "future answer")
	builder.FinishTurn("future")
	sourceAfter := mustRecords(t, builder.Thread())

	exported := checkpointRecords(t, checkpoint)
	exported[0].Payload[0] = '!'
	exported[0].ID = "caller changed identity"
	exported = append(exported, transcript.Record{ID: "extra"})
	if len(exported) != len(want)+1 {
		t.Fatal("test did not append exported record")
	}
	if got := checkpointRecords(t, checkpoint); !reflect.DeepEqual(got, want) {
		t.Fatal("checkpoint records changed through an exported alias or source mutation")
	}

	for range 2 {
		restored, err := checkpoint.Restore()
		if err != nil {
			t.Fatal(err)
		}
		if restored.Revision() != before.Revision() || checkpoint.Revision() != before.Revision() {
			t.Fatal("checkpoint revision changed")
		}
		if got := mustRecords(t, restored); !reflect.DeepEqual(got, want) {
			t.Fatal("restored records differ from captured records")
		}
		child := transcript.NewBuilderFrom(restored)
		child.SetPlan("child", code.Plan{Steps: []code.PlanStep{{Text: "child plan", Status: code.StepStatuses.PENDING}}})
		child.AddUser("child prompt")
		if err := child.Thread().Validate(); err != nil {
			t.Fatal(err)
		}
		entries := restored.Entries()
		for i := range entries {
			entries[i].Payload.Text = "caller changed text"
			if len(entries[i].Payload.Attachments) != 0 {
				entries[i].Payload.Attachments[0].Name = "caller changed attachment"
			}
		}
	}
	if got := mustRecords(t, builder.Thread()); !reflect.DeepEqual(got, sourceAfter) {
		t.Fatal("restoring or continuing a checkpoint modified the source thread")
	}
}

func TestCheckpointFromRecordsPreservesExactPayloadBytes(t *testing.T) {
	t.Parallel()

	payload := []byte("{ \"text\" : \"historical prompt\" }\n")
	records := []transcript.Record{
		{Sequence: 2, ID: "e2", Kind: "assistant_message", TurnID: "turn", Revision: 3, Payload: []byte(`{"complete":true,"text":"answer"}`)},
		{Sequence: 1, ID: "e1", Kind: "user_message", Revision: 1, Payload: bytes.Clone(payload)},
	}
	checkpoint, err := transcript.CheckpointFromRecords(3, records)
	if err != nil {
		t.Fatal(err)
	}
	records[1].Payload[0] = '!'
	records[0].ID = "changed"
	got := checkpointRecords(t, checkpoint)
	if got[0].Sequence != 1 || !bytes.Equal(got[0].Payload, payload) || got[1].ID != "e2" {
		t.Fatal("checkpoint lost ordering, exact bytes, or input ownership")
	}
	loaded, err := transcript.CheckpointFromRecords(checkpoint.Revision(), got)
	if err != nil {
		t.Fatal(err)
	}
	got[0].Payload[0] = '!'
	if again := checkpointRecords(t, loaded); !bytes.Equal(again[0].Payload, payload) {
		t.Fatal("loaded checkpoint aliases exported payloads")
	}
}

func TestCheckpointRejectsUnsettledCanonicalState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(*transcript.Builder)
	}{
		{"admitted assistant before first token", func(b *transcript.Builder) { b.StartTurn("turn", "") }},
		{"streaming assistant", func(b *transcript.Builder) { b.AppendAssistant("turn", "", "partial") }},
		{"reasoning", func(b *transcript.Builder) { b.AppendReasoning("turn", "", "thinking") }},
		{"running tool", func(b *transcript.Builder) { b.StartTool("turn", "", "tool", "", "read", "main.go", 0) }},
		{"pending subagent", func(b *transcript.Builder) { b.ReserveSubagent("spawn", "researcher", "inspect") }},
		{"running subagent", func(b *transcript.Builder) {
			b.StartSubagent("child", "spawn", "researcher", "provider", "model", "inspect")
		}},
		{"undelivered queue", func(b *transcript.Builder) { b.AddQueuedUser("later") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			builder := transcript.NewBuilder()
			builder.AddUser("prompt")
			tt.setup(builder)
			before := mustRecords(t, builder.Thread())
			checkpoint, err := builder.Thread().CaptureCheckpoint()
			if !errors.Is(err, transcript.ErrCheckpointUnsettled) {
				t.Fatalf("CaptureCheckpoint error = %v", err)
			}
			if _, err := checkpoint.Restore(); !errors.Is(err, transcript.ErrCheckpointUnavailable) {
				t.Fatalf("rejected checkpoint Restore error = %v", err)
			}
			if got := mustRecords(t, builder.Thread()); !reflect.DeepEqual(got, before) {
				t.Fatal("rejected capture mutated source records")
			}
		})
	}
}

func TestCheckpointAcceptsExplicitlyInterruptedState(t *testing.T) {
	t.Parallel()

	builder := transcript.NewBuilder()
	builder.AddUser("cancelled prompt")
	builder.AppendAssistant("turn", "", "partial")
	builder.StartTool("turn", "", "tool", "", "read", "main.go", 0)
	builder.ReserveSubagent("spawn", "researcher", "inspect")
	settled, changed := builder.Thread().RecoverInterrupted()
	if !changed {
		t.Fatal("expected explicit interruption transitions")
	}
	checkpoint, err := settled.CaptureCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := transcript.CheckpointFromRecords(checkpoint.Revision(), checkpointRecords(t, checkpoint))
	if err != nil {
		t.Fatal(err)
	}
	restored, err := loaded.Restore()
	if err != nil {
		t.Fatal(err)
	}
	if restored.Revision() != settled.Revision() || !reflect.DeepEqual(mustRecords(t, restored), mustRecords(t, settled)) {
		t.Fatal("explicitly interrupted state was repaired again")
	}
}

func TestCheckpointFromRecordsRejectsWithoutRepairOrPrivateDiagnostics(t *testing.T) {
	t.Parallel()

	const canary = "PRIVATE_CHECKPOINT_CANARY"
	tests := []struct {
		name     string
		kind     string
		payload  string
		parentID string
		sequence uint64
		revision uint64
		wantErr  error
	}{
		{name: "crash-open empty assistant", kind: "assistant_message", payload: `{}`, wantErr: transcript.ErrInvalidCheckpoint},
		{name: "active assistant", kind: "assistant_message", payload: `{"text":"partial"}`, wantErr: transcript.ErrCheckpointUnsettled},
		{name: "active reasoning", kind: "reasoning", payload: `{"text":"partial"}`, wantErr: transcript.ErrCheckpointUnsettled},
		{name: "active tool", kind: "tool_call", payload: `{"tool_id":"tool","tool_name":"read","tool_state":"running"}`, wantErr: transcript.ErrCheckpointUnsettled},
		{name: "pending child", kind: "subagent", payload: `{"agent_name":"researcher","subagent":"pending"}`, wantErr: transcript.ErrCheckpointUnsettled},
		{name: "pending input", kind: "queued_user", payload: `{"text":"later"}`, wantErr: transcript.ErrCheckpointUnsettled},
		{name: "contradictory assistant", kind: "assistant_message", payload: `{"text":"answer","complete":true,"interrupted":true}`, wantErr: transcript.ErrInvalidCheckpoint},
		{name: "contradictory reasoning", kind: "reasoning", payload: `{"text":"thought","complete":true,"interrupted":true}`, wantErr: transcript.ErrInvalidCheckpoint},
		{name: "notice carrying tool state", kind: "notice", payload: `{"text":"notice","tool_state":"running","tool_id":"` + canary + `"}`, wantErr: transcript.ErrInvalidCheckpoint},
		{name: "user carrying child state", kind: "user_message", payload: `{"text":"prompt","subagent":"pending","agent_name":"` + canary + `"}`, wantErr: transcript.ErrInvalidCheckpoint},
		{name: "injected assistant", kind: "assistant_message", payload: `{"text":"answer","complete":true,"injected":true}`, wantErr: transcript.ErrInvalidCheckpoint},
		{name: "unknown kind", kind: canary, payload: `{}`, wantErr: transcript.ErrInvalidCheckpoint},
		{name: "unknown payload field", kind: "notice", payload: `{"text":"notice","` + canary + `":true}`, wantErr: transcript.ErrInvalidCheckpoint},
		{name: "invalid JSON", kind: "notice", payload: canary, wantErr: transcript.ErrInvalidCheckpoint},
		{name: "trailing JSON", kind: "notice", payload: `{"text":"notice"} "` + canary + `"`, wantErr: transcript.ErrInvalidCheckpoint},
		{name: "orphan", kind: "notice", payload: `{"text":"notice"}`, parentID: canary, wantErr: transcript.ErrInvalidCheckpoint},
		{name: "sequence gap", kind: "notice", payload: `{"text":"notice"}`, sequence: 2, wantErr: transcript.ErrInvalidCheckpoint},
		{name: "future revision", kind: "notice", payload: `{"text":"notice"}`, revision: 2, wantErr: transcript.ErrInvalidCheckpoint},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			record := transcript.Record{Sequence: 1, ID: canary, Kind: tt.kind, Revision: 1, ParentID: tt.parentID, Payload: []byte(tt.payload)}
			if tt.sequence != 0 {
				record.Sequence = tt.sequence
			}
			if tt.revision != 0 {
				record.Revision = tt.revision
			}
			records := []transcript.Record{record}
			checkpoint, err := transcript.CheckpointFromRecords(1, records)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CheckpointFromRecords error = %v, want %v", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), canary) {
				t.Fatal("checkpoint diagnostic disclosed private record data")
			}
			if _, err := checkpoint.Restore(); !errors.Is(err, transcript.ErrCheckpointUnavailable) {
				t.Fatalf("rejected checkpoint Restore error = %v", err)
			}
			if !reflect.DeepEqual(records[0], record) || string(records[0].Payload) != tt.payload {
				t.Fatal("rejection changed input records")
			}
		})
	}
}

func TestCheckpointRejectsMalformedLegacyThreadWithoutChangingResume(t *testing.T) {
	t.Parallel()

	records := []transcript.Record{{
		Sequence: 1, ID: "e1", Kind: "notice", Revision: 1,
		Payload: []byte(`{"text":"legacy notice","tool_state":"running"}`),
	}}
	legacy, err := transcript.FromRecords(1, records)
	if err != nil {
		t.Fatalf("legacy resume changed: %v", err)
	}
	before := mustRecords(t, legacy)
	checkpoint, err := legacy.CaptureCheckpoint()
	if !errors.Is(err, transcript.ErrInvalidCheckpoint) {
		t.Fatalf("malformed legacy capture error = %v", err)
	}
	if _, err := checkpoint.Restore(); !errors.Is(err, transcript.ErrCheckpointUnavailable) {
		t.Fatalf("rejected legacy checkpoint restore error = %v", err)
	}
	if !reflect.DeepEqual(mustRecords(t, legacy), before) || legacy.Revision() != 1 {
		t.Fatal("rejected checkpoint changed legacy source")
	}
}

func checkpointRecords(t *testing.T, checkpoint transcript.Checkpoint) []transcript.Record {
	t.Helper()
	records, err := checkpoint.Records()
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func settledCheckpointBuilder() *transcript.Builder {
	builder := transcript.NewBuilder()
	builder.AddUserWithAttachments("first prompt", []transcript.Attachment{{Name: "image.png", MIMEType: "image/png", Size: 10}})
	builder.AppendAssistant("turn", "", "answer")
	builder.AppendReasoning("turn", "", "reasoning")
	builder.AddSkill("turn", "", "go-style")
	builder.SetPlan("turn", code.Plan{Steps: []code.PlanStep{{Text: "historical plan", Status: code.StepStatuses.PENDING}}})
	parentID := builder.StartToolExecution("turn", "", "parent-execution", "reused-provider-id", "", "", "program", "read files", 0)
	builder.StartToolExecution("turn", parentID, "nested-execution", "reused-provider-id", "parent-execution", "reused-provider-id", "read", "main.go", 1)
	builder.FinishToolExecution("nested-execution", "reused-provider-id", "read content", "", 1, false)
	builder.FinishToolExecution("parent-execution", "reused-provider-id", "program completed", "", 2, false)
	childID := builder.StartSubagent("child", "spawn", "researcher", "provider", "model", "inspect")
	builder.AppendAssistant("child", childID, "child answer")
	builder.FinishTurn("child")
	builder.FinishSubagent("child", transcript.SubagentCompleted)
	builder.FinishTurn("turn")
	queuedID := builder.AddQueuedUser("steer")
	builder.InjectQueuedUser(queuedID, "steer")
	builder.AppendAssistant("turn", "", "steered answer")
	builder.FinishTurn("turn")
	return builder
}
