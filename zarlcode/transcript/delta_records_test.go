package transcript_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
)

func TestEventRecordsMatchCanonicalDelta(t *testing.T) {
	t.Parallel()
	r := transcript.NewReducer()
	apply := func(event any) transcript.Change {
		t.Helper()
		change, err := r.Apply(event)
		if err != nil {
			t.Fatalf("%T: %v", event, err)
		}
		want, err := r.Thread().RecordsSince(change.BeforeRevision)
		if err != nil {
			t.Fatal(err)
		}
		if len(change.Records) != len(want) {
			t.Fatalf("%T: records %d, want %d", event, len(change.Records), len(want))
		}
		for i := range want {
			if !reflect.DeepEqual(change.Records[i], want[i]) {
				t.Fatalf("%T: record %d differs from canonical delta", event, i)
			}
		}
		return change
	}
	for _, event := range []any{
		transcript.UserSubmitted{EntryID: "e100", Text: "prompt"},
		transcript.AssistantDelta{TurnID: "root", Delta: "first"},
		transcript.AssistantSegmentFinished{TurnID: "root"},
		transcript.AssistantSegmentFinished{TurnID: "root"},
		transcript.ReasoningDelta{TurnID: "root", Delta: "reason"},
		transcript.SkillLoaded{TurnID: "root", Name: "skill"},
		transcript.SkillLoaded{TurnID: "root", Name: "skill"},
		transcript.ControlRecorded{TurnID: "root", Control: testControl("control")},
		transcript.ControlRecorded{TurnID: "root", Control: testControl("control")},
		transcript.ToolStarted{TurnID: "root", ExecutionID: "call", ToolID: "call", Name: "bash"},
		transcript.ToolOutputUpdated{ExecutionID: "call", ToolID: "call", Output: "one"},
		transcript.ToolOutputUpdated{ExecutionID: "call", ToolID: "call", Output: "one"},
		transcript.SubagentStarted{TurnID: "child", SpawnExecutionID: "spawn", SpawnToolID: "spawn", AgentName: "worker", Prompt: "task"},
		transcript.AssistantDelta{TurnID: "child", Delta: "answer"},
		transcript.ReasoningDelta{TurnID: "child", Delta: "reason"},
		transcript.SubagentFinished{TurnID: "child", Status: transcript.SubagentCompleted},
		transcript.TurnFinished{TurnID: "root"},
	} {
		apply(event)
	}
	queued := apply(transcript.QueuedUserAdded{Text: "queued"})
	apply(transcript.QueuedUserInjected{EntryID: queued.PrimaryEntryID, Text: "injected"})
	r = transcript.NewReducerFrom(r.Thread())
	apply(transcript.ToolOutputUpdated{ExecutionID: "call", ToolID: "call", Output: "restored"})
	if _, err := r.Apply(struct{}{}); !errors.Is(err, transcript.ErrUnsupportedEvent) {
		t.Fatalf("unsupported: %v", err)
	}
	apply(transcript.ToolFinished{ExecutionID: "call", ToolID: "call"})
	cleared, err := r.Apply(transcript.Clear{})
	if err != nil || len(cleared.Records) != 0 || !r.Thread().IsEmpty() {
		t.Fatal("clear retained changes")
	}
	apply(transcript.UserSubmitted{EntryID: "e100", Text: "fresh prompt"})
	apply(transcript.NoticeAdded{Text: "fresh notice"})
}

func TestReducerEmptinessTracksCurrentEntries(t *testing.T) {
	t.Parallel()
	r := transcript.NewReducer()
	if !r.IsEmpty() {
		t.Fatal("new reducer is not empty")
	}
	if _, err := r.Apply(transcript.UserSubmitted{Text: "prompt"}); err != nil {
		t.Fatal(err)
	}
	if r.IsEmpty() || transcript.NewReducerFrom(r.Thread()).IsEmpty() {
		t.Fatal("populated or restored reducer is empty")
	}
	if _, err := r.Apply(transcript.Clear{}); err != nil {
		t.Fatal(err)
	}
	if !r.IsEmpty() {
		t.Fatal("cleared reducer is not empty")
	}
}
