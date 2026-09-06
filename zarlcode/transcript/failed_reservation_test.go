package transcript_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
)

func TestFailedUnstartedReservationRecordsRoundTrip(t *testing.T) {
	t.Parallel()

	builder := transcript.NewBuilder()
	builder.ReserveSubagent("spawn", "reviewer", "inspect the failure")
	builder.FailSubagent("spawn", "child did not start")
	want := builder.Thread()
	if err := want.Validate(); err != nil {
		t.Fatalf("Validate failed reservation: %v", err)
	}

	records := mustRecords(t, want)
	got, err := transcript.FromRecords(want.Revision(), records)
	if err != nil {
		t.Fatalf("FromRecords failed reservation: %v", err)
	}
	if !reflect.DeepEqual(got.Entries(), want.Entries()) {
		t.Fatalf("round-trip entries = %#v, want %#v", got.Entries(), want.Entries())
	}
	if got.Revision() != want.Revision() {
		t.Fatalf("round-trip revision = %d, want %d", got.Revision(), want.Revision())
	}
	if roundTrip := mustRecords(t, got); !reflect.DeepEqual(roundTrip, records) {
		t.Fatalf("round-trip records = %#v, want %#v", roundTrip, records)
	}
}

func TestPendingReservationRecoveryRecordsRoundTrip(t *testing.T) {
	t.Parallel()

	builder := transcript.NewBuilder()
	builder.ReserveSubagent("spawn", "reviewer", "inspect after restart")
	pending := builder.Thread()
	if entries := pending.Entries(); len(entries) != 1 {
		t.Fatalf("pending subsections = %d, want 1", len(entries))
	}

	recovered, changed := pending.RecoverInterrupted()
	if !changed {
		t.Fatal("RecoverInterrupted did not advance pending reservation")
	}
	if err := recovered.Validate(); err != nil {
		t.Fatalf("Validate recovered reservation: %v", err)
	}
	entries := recovered.Entries()
	if len(entries) != 1 {
		t.Fatalf("recovered subsections = %d, want 1", len(entries))
	}
	reservation := entries[0]
	if reservation.TurnID != "" || reservation.Payload.Subagent != transcript.SubagentInterrupted ||
		reservation.Payload.SpawnToolID != "spawn" {
		t.Fatalf("recovered reservation = %#v, want interrupted unstarted spawn", reservation)
	}

	records := mustRecords(t, recovered)
	if len(records) != 1 {
		t.Fatalf("records = %d, want exactly one subsection", len(records))
	}
	restored, err := transcript.FromRecords(recovered.Revision(), records)
	if err != nil {
		t.Fatalf("FromRecords recovered reservation: %v", err)
	}
	if restoredEntries := restored.Entries(); len(restoredEntries) != 1 || !reflect.DeepEqual(restoredEntries, entries) {
		t.Fatalf("restored subsections = %#v, want exactly %#v", restoredEntries, entries)
	}

	unchanged, changed := restored.RecoverInterrupted()
	if changed || unchanged.Revision() != restored.Revision() || !reflect.DeepEqual(unchanged.Entries(), restored.Entries()) {
		t.Fatalf("second recovery changed=%v thread=%#v, want no-op %#v", changed, unchanged, restored)
	}
}

func TestFromRecordsRestoresFailedSpawnSubsection(t *testing.T) {
	t.Parallel()

	thread, err := transcript.FromRecords(2, []transcript.Record{
		record(1, "e1", "", "user_message", 1, `{"text":"start a reviewer"}`),
		record(2, "e2", "", "subagent", 2, `{"text":"child did not start","agent_name":"reviewer","prompt":"inspect","spawn_tool_id":"spawn","subagent":"failed"}`),
	})
	if err != nil {
		t.Fatalf("FromRecords failed spawn subsection: %v", err)
	}

	entries := thread.Entries()
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	failed := entries[1]
	if failed.ParentID != "" || failed.TurnID != "" {
		t.Fatalf("failed spawn identity = parent %q turn %q, want unstarted top-level reservation", failed.ParentID, failed.TurnID)
	}
	if failed.Payload.Subagent != transcript.SubagentFailed || failed.Payload.SpawnToolID != "spawn" ||
		failed.Payload.AgentName != "reviewer" || failed.Payload.Prompt != "inspect" || failed.Payload.Text != "child did not start" {
		t.Fatalf("failed spawn subsection = %#v", failed)
	}

	resumed := transcript.NewBuilderFrom(thread).Thread()
	if !reflect.DeepEqual(resumed.Entries(), entries) {
		t.Fatalf("resumed entries = %#v, want %#v", resumed.Entries(), entries)
	}
}

func TestFromRecordsRejectsMalformedUnstartedSubagents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		state   transcript.SubagentState
		payload string
		want    string
	}{
		{
			name:    "failed without spawn reference",
			state:   transcript.SubagentFailed,
			payload: `{"agent_name":"reviewer","subagent":"failed"}`,
			want:    "failed unstarted subagent spawn reference is empty",
		},
		{
			name:    "interrupted without spawn reference",
			state:   transcript.SubagentInterrupted,
			payload: `{"agent_name":"reviewer","subagent":"interrupted"}`,
			want:    "interrupted unstarted subagent spawn reference is empty",
		},
		{
			name:    "running without child ID",
			state:   transcript.SubagentRunning,
			payload: `{"agent_name":"reviewer","spawn_tool_id":"spawn","subagent":"running"}`,
			want:    "running subagent turn ID is empty",
		},
		{
			name:    "completed without child ID",
			state:   transcript.SubagentCompleted,
			payload: `{"agent_name":"reviewer","spawn_tool_id":"spawn","subagent":"completed"}`,
			want:    "completed subagent turn ID is empty",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := transcript.FromRecords(1, []transcript.Record{
				record(1, "e1", "", "subagent", 1, tc.payload),
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("FromRecords(%s) error = %v, want %q", tc.state, err, tc.want)
			}
		})
	}
}

func TestFromRecordsStillRejectsDuplicateEntryIDs(t *testing.T) {
	t.Parallel()

	_, err := transcript.FromRecords(2, []transcript.Record{
		record(1, "duplicate", "", "user_message", 1, `{"text":"first"}`),
		record(2, "duplicate", "", "notice", 2, `{"text":"second"}`),
	})
	if err == nil || !strings.Contains(err.Error(), `duplicate entry ID "duplicate"`) {
		t.Fatalf("error = %v, want duplicate entry ID rejection", err)
	}
}
