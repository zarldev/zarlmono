package transcript_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestThreadSnapshotsDoNotAliasMutablePayloads(t *testing.T) {
	plan := code.Plan{Steps: []code.PlanStep{{Text: "original plan", Status: code.StepStatuses.PENDING}}}
	builder := transcript.NewBuilder()
	builder.SetPlan("turn", plan)
	builder.AddSkill("turn", "", "original-skill")

	plan.Steps[0].Text = "mutated input"
	entries := builder.Thread().Entries()
	for i := range entries {
		switch entries[i].Kind {
		case transcript.EntryKinds.ENTRYPLAN:
			entries[i].Payload.Plan.Steps[0].Text = "mutated snapshot"
		case transcript.EntryKinds.ENTRYSKILLS:
			entries[i].Payload.Skills[0] = "mutated-snapshot"
		}
	}

	for _, entry := range builder.Thread().Entries() {
		switch entry.Kind {
		case transcript.EntryKinds.ENTRYPLAN:
			if entry.Payload.Plan.Steps[0].Text != "original plan" {
				t.Fatalf("canonical plan = %q", entry.Payload.Plan.Steps[0].Text)
			}
		case transcript.EntryKinds.ENTRYSKILLS:
			if entry.Payload.Skills[0] != "original-skill" {
				t.Fatalf("canonical skill = %q", entry.Payload.Skills[0])
			}
		}
	}
}

func TestNewBuilderFromPreservesCanonicalPlanIdentity(t *testing.T) {
	builder := transcript.NewBuilder()
	builder.SetPlan("turn-1", code.Plan{Steps: []code.PlanStep{{Text: "first", Status: code.StepStatuses.INPROGRESS}}})
	resumed := transcript.NewBuilderFrom(builder.Thread())
	resumed.SetPlan("turn-2", code.Plan{Steps: []code.PlanStep{{Text: "latest", Status: code.StepStatuses.COMPLETED}}})

	entries := resumed.Thread().Entries()
	plans := 0
	for _, entry := range entries {
		if entry.Kind != transcript.EntryKinds.ENTRYPLAN {
			continue
		}
		plans++
		if entry.TurnID != "turn-2" || entry.Payload.Plan.Steps[0].Text != "latest" {
			t.Fatalf("plan entry = %#v", entry)
		}
	}
	if plans != 1 {
		t.Fatalf("plan entries = %d, want 1", plans)
	}
}
func TestRecordsSinceUsesCompactSnakeCasePayload(t *testing.T) {
	t.Parallel()

	builder := transcript.NewBuilder()
	builder.StartTool("turn", "", "tool", "", "read", "main.go", 0)
	records, err := builder.Thread().RecordsSince(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %#v", records)
	}
	got := string(records[0].Payload)
	want := `{"tool_id":"tool","tool_name":"read","argument":"main.go","tool_state":"running"}`
	if got != want {
		t.Fatalf("payload = %s, want %s", got, want)
	}
	if strings.Contains(got, "duration_ms") || strings.Contains(got, "complete") || strings.Contains(got, "plan") {
		t.Fatalf("payload contains zero-value fields: %s", got)
	}
}

func TestSuccessfulToolOmitsFailureKind(t *testing.T) {
	t.Parallel()

	builder := transcript.NewBuilder()
	builder.StartTool("turn", "", "tool", "", "read", "main.go", 0)
	builder.FinishTool("tool", "read main.go", "unknown", 10, false)

	thread := builder.Thread()
	if err := thread.Validate(); err != nil {
		t.Fatal(err)
	}
	records, err := thread.RecordsSince(0)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(records[0].Payload); strings.Contains(got, "failure_kind") {
		t.Fatalf("successful tool payload contains failure kind: %s", got)
	}
}

func TestRecordsSinceReturnsOnlyChangedEntries(t *testing.T) {
	builder := transcript.NewBuilder()
	builder.AddUser("prompt")
	base := builder.Thread().Revision()
	builder.StartTurn("turn", "")
	builder.AppendAssistant("turn", "", "partial")
	records, err := builder.Thread().RecordsSince(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Kind != "assistant_message" {
		t.Fatalf("records = %#v", records)
	}
	thread, err := transcript.FromRecords(builder.Thread().Revision(), append(mustRecords(t, transcript.NewBuilderFrom(transcriptThreadWithUser(t)).Thread(), 0), records...))
	if err != nil {
		t.Fatal(err)
	}
	if thread.Revision() != builder.Thread().Revision() {
		t.Fatalf("revision = %d, want %d", thread.Revision(), builder.Thread().Revision())
	}
}

func transcriptThreadWithUser(t *testing.T) transcript.Thread {
	t.Helper()
	builder := transcript.NewBuilder()
	builder.AddUser("prompt")
	return builder.Thread()
}
func TestNewBuilderFromContinuesActiveSemanticEntries(t *testing.T) {
	builder := transcript.NewBuilder()
	builder.StartTurn("turn", "")
	builder.AppendAssistant("turn", "", "first")
	builder.AppendReasoning("turn", "", "thought")
	builder.AddSkill("turn", "", "one")

	resumed := transcript.NewBuilderFrom(builder.Thread())
	resumed.AppendAssistant("turn", "", " second")
	resumed.AppendReasoning("turn", "", " more")
	resumed.AddSkill("turn", "", "two")
	if err := resumed.Thread().Validate(); err != nil {
		t.Fatal(err)
	}

	counts := make(map[transcript.EntryKind]int)
	for _, entry := range resumed.Thread().Entries() {
		counts[entry.Kind]++
	}
	for _, kind := range []transcript.EntryKind{
		transcript.EntryKinds.ENTRYASSISTANTMESSAGE,
		transcript.EntryKinds.ENTRYREASONING,
		transcript.EntryKinds.ENTRYSKILLS,
	} {
		if counts[kind] != 1 {
			t.Fatalf("%s entries = %d, want 1", kind, counts[kind])
		}
	}
}

func TestNewBuilderFromAdvancesGeneratedEntryIdentity(t *testing.T) {
	thread, err := transcript.FromRecords(2, []transcript.Record{
		record(1, "e1", "", "user_message", 1, `{"text":"first"}`),
		record(2, "e7", "", "notice", 2, `{"text":"seventh"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	builder := transcript.NewBuilderFrom(thread)
	builder.AddUser("next")
	entries := builder.Thread().Entries()
	if got := entries[len(entries)-1].ID; got != "e8" {
		t.Fatalf("next entry ID = %q, want e8", got)
	}
}

func TestFromRecordsRejectsDuplicateSemanticIdentities(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		records []transcript.Record
		want    string
	}{
		{
			name: "tool ID",
			records: []transcript.Record{
				record(1, "e1", "turn", "tool_call", 1, `{"tool_id":"tool","tool_name":"read","tool_state":"running"}`),
				record(2, "e2", "turn", "tool_call", 2, `{"tool_id":"tool","tool_name":"read","tool_state":"running"}`),
			},
			want: "duplicate tool ID",
		},
		{
			name: "turn assistant",
			records: []transcript.Record{
				record(1, "e1", "turn", "assistant_message", 1, `{"text":"first","complete":true}`),
				record(2, "e2", "turn", "assistant_message", 2, `{"text":"second","complete":true}`),
			},
			want: "duplicate assistant entry",
		},
		{
			name: "plan",
			records: []transcript.Record{
				record(1, "e1", "turn-1", "plan", 1, `{"plan":{"steps":[{"step":"first","status":"pending"}]}}`),
				record(2, "e2", "turn-2", "plan", 2, `{"plan":{"steps":[{"step":"second","status":"pending"}]}}`),
			},
			want: "multiple plan entries",
		},
		{
			name: "subagent turn ID",
			records: []transcript.Record{
				record(1, "e1", "child", "subagent", 1, `{"agent_name":"reviewer","subagent":"running"}`),
				record(2, "e2", "child", "subagent", 2, `{"agent_name":"tester","subagent":"running"}`),
			},
			want: "duplicate subagent turn ID",
		},
		{
			name: "pending subagent with turn ID",
			records: []transcript.Record{
				record(1, "e1", "child", "subagent", 1, `{"agent_name":"reviewer","subagent":"pending"}`),
			},
			want: "pending subagent has turn ID",
		},
		{
			name: "running subagent without turn ID",
			records: []transcript.Record{
				record(1, "e1", "", "subagent", 1, `{"agent_name":"reviewer","subagent":"running"}`),
			},
			want: "running subagent turn ID is empty",
		},
		{
			name: "subagent spawn tool ID",
			records: []transcript.Record{
				record(1, "e1", "child-1", "subagent", 1, `{"agent_name":"reviewer","spawn_tool_id":"spawn","subagent":"running"}`),
				record(2, "e2", "child-2", "subagent", 2, `{"agent_name":"tester","spawn_tool_id":"spawn","subagent":"running"}`),
			},
			want: "duplicate subagent spawn tool ID",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := transcript.FromRecords(2, tc.records)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func record(sequence uint64, id, turnID, kind string, revision uint64, payload string) transcript.Record {
	return transcript.Record{
		Sequence: sequence, ID: id, TurnID: turnID,
		Kind: kind, Revision: revision, Payload: []byte(payload),
	}
}

func mustRecords(t *testing.T, thread transcript.Thread, since uint64) []transcript.Record {
	t.Helper()
	records, err := thread.RecordsSince(since)
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func TestFromRecordsRejectsRevisionMetadataMismatch(t *testing.T) {
	records := []transcript.Record{{Sequence: 1, ID: "e1", Kind: "user_message", Revision: 1, Payload: []byte(`{"text":"prompt"}`)}}
	_, err := transcript.FromRecords(2, records)
	if err == nil || !strings.Contains(err.Error(), "does not match thread revision") {
		t.Fatalf("error = %v, want revision mismatch", err)
	}
}

func TestFromRecordsRejectsUnknownPayloadField(t *testing.T) {
	records := []transcript.Record{{Sequence: 1, ID: "e1", Kind: "user_message", Revision: 1, Payload: []byte(`{"text":"prompt","expanded":true}`)}}
	_, err := transcript.FromRecords(1, records)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("error = %v, want unknown field", err)
	}
}
func TestFromRecordsRejectsTrailingPayloadData(t *testing.T) {
	t.Parallel()

	for _, payload := range []string{
		`{"text":"prompt"} {"text":"extra"}`,
		`{"text":"prompt"} garbage`,
	} {
		records := []transcript.Record{{Sequence: 1, ID: "e1", Kind: "user_message", Revision: 1, Payload: []byte(payload)}}
		_, err := transcript.FromRecords(1, records)
		if err == nil || !strings.Contains(err.Error(), "trailing payload data") {
			t.Fatalf("payload %q error = %v, want trailing payload data", payload, err)
		}
	}
}

func TestFromRecordsRejectsInvalidParentKind(t *testing.T) {
	records := []transcript.Record{
		{Sequence: 1, ID: "e1", Kind: "user_message", Revision: 1, Payload: []byte(`{"text":"prompt"}`)},
		{Sequence: 2, ID: "e2", ParentID: "e1", Kind: "notice", Revision: 2, Payload: []byte(`{"text":"nested"}`)},
	}
	_, err := transcript.FromRecords(2, records)
	if err == nil || !strings.Contains(err.Error(), "invalid parent kind") {
		t.Fatalf("error = %v, want invalid parent kind", err)
	}
}

func TestFromRecordsRejectsInvalidToolPayload(t *testing.T) {
	records := []transcript.Record{{Sequence: 1, ID: "e1", Kind: "tool_call", Revision: 1, Payload: []byte(`{"tool_name":"read","tool_state":"mystery"}`)}}
	_, err := transcript.FromRecords(1, records)
	if err == nil || !strings.Contains(err.Error(), "tool identity is incomplete") {
		t.Fatalf("error = %v, want invalid tool payload", err)
	}
}
func TestFromRecordsRejectsContradictoryLifecyclePayloads(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		kind    string
		payload string
		want    string
	}{
		{name: "running tool duration", kind: "tool_call", payload: `{"tool_id":"tool","tool_name":"read","tool_state":"running","duration_ms":1}`, want: "running tool has duration"},
		{name: "successful tool failure", kind: "tool_call", payload: `{"tool_id":"tool","tool_name":"read","tool_state":"succeeded","failure_kind":"error"}`, want: "succeeded tool has failure kind"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := transcript.FromRecords(1, []transcript.Record{record(1, "e1", "turn", tc.kind, 1, tc.payload)})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestFromRecordsRecoversEmptyOpenAssistant(t *testing.T) {
	t.Parallel()

	thread, err := transcript.FromRecords(1, []transcript.Record{
		record(1, "e1", "turn", "assistant_message", 1, `{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	entries := thread.Entries()
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if !entries[0].Payload.Interrupted || entries[0].Payload.Complete {
		t.Fatalf("assistant lifecycle = %+v, want interrupted and incomplete", entries[0].Payload)
	}
	if thread.Revision() != 2 {
		t.Fatalf("revision = %d, want 2", thread.Revision())
	}
}

func TestRecoverInterruptedAdvancesOpenLifecycleEntries(t *testing.T) {
	builder := transcript.NewBuilder()
	builder.AddUser("prompt")
	builder.StartTurn("turn", "")
	builder.AppendReasoning("turn", "", "partial thought")
	builder.AppendAssistant("turn", "", "partial answer")
	builder.StartTool("turn", "", "tool", "", "read", "main.go", 0)
	builder.StartSubagent("child", "spawn", "explore", "anthropic", "claude-sonnet", "inspect")
	before := builder.Thread()
	recovered, changed := before.RecoverInterrupted()
	if !changed || recovered.Revision() <= before.Revision() {
		t.Fatalf("recovery changed=%v revision=%d before=%d", changed, recovered.Revision(), before.Revision())
	}
	var assistant, reasoning, tool, child bool
	for _, entry := range recovered.Entries() {
		switch entry.Kind {
		case transcript.EntryKinds.ENTRYASSISTANTMESSAGE:
			assistant = entry.Payload.Interrupted && !entry.Payload.Complete
		case transcript.EntryKinds.ENTRYREASONING:
			reasoning = entry.Payload.Interrupted && !entry.Payload.Complete
		case transcript.EntryKinds.ENTRYTOOLCALL:
			tool = entry.Payload.ToolState == transcript.ToolInterrupted
		case transcript.EntryKinds.ENTRYSUBAGENT:
			child = entry.Payload.Subagent == transcript.SubagentInterrupted &&
				entry.Payload.Provider == "anthropic" && entry.Payload.Model == "claude-sonnet"
		}
	}
	if !assistant || !reasoning || !tool || !child {
		t.Fatalf("recovered lifecycle assistant=%v reasoning=%v tool=%v child=%v", assistant, reasoning, tool, child)
	}
}

func TestRecoverInterruptedIsIdempotent(t *testing.T) {
	builder := transcript.NewBuilder()
	builder.StartTurn("turn", "")
	builder.AppendAssistant("turn", "", "partial")
	first, changed := builder.Thread().RecoverInterrupted()
	if !changed {
		t.Fatal("first recovery did not change thread")
	}
	second, changed := first.RecoverInterrupted()
	if changed || second.Revision() != first.Revision() {
		t.Fatalf("second recovery changed=%v revision=%d want=%d", changed, second.Revision(), first.Revision())
	}
}
