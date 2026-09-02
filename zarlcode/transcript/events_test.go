package transcript_test

import (
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestUserSubmittedAcceptsAttachmentOnlyMetadata(t *testing.T) {
	t.Parallel()
	reducer := transcript.NewReducer()
	_, err := reducer.Apply(transcript.UserSubmitted{
		Attachments: []transcript.Attachment{{Name: "image.png", MIMEType: "image/png", Size: 42}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := reducer.Thread().Entries()
	if len(got) != 1 || len(got[0].Payload.Attachments) != 1 {
		t.Fatalf("entries = %#v", got)
	}
}

func TestReducerAppliesSemanticEventsAndReturnsCanonicalChanges(t *testing.T) {
	reducer := transcript.NewReducer()
	user, err := reducer.Apply(transcript.UserSubmitted{Text: "prompt"})
	if err != nil {
		t.Fatal(err)
	}
	if !user.Changed() || user.Persistence != transcript.Persistences.PERSISTENCEIMMEDIATE || user.AfterRevision != 1 || len(user.Records) != 1 || user.Records[0].Kind != "user_message" {
		t.Fatalf("user change = %#v", user)
	}
	start, err := reducer.Apply(transcript.TurnStarted{TurnID: "turn"})
	if err != nil {
		t.Fatal(err)
	}
	if !start.Changed() || len(start.Records) != 1 || start.Records[0].Kind != "assistant_message" {
		t.Fatalf("start change = %#v", start)
	}
	delta, err := reducer.Apply(transcript.AssistantDelta{TurnID: "turn", Delta: "answer"})
	if err != nil {
		t.Fatal(err)
	}
	if !delta.Changed() || delta.Persistence != transcript.Persistences.PERSISTENCEDEBOUNCED || len(delta.Records) != 1 || delta.Records[0].ID != start.Records[0].ID {
		t.Fatalf("delta change = %#v", delta)
	}
}

func TestReducerRejectsUnsupportedEventWithoutMutation(t *testing.T) {
	reducer := transcript.NewReducer()
	before := reducer.Thread()
	change, err := reducer.Apply(struct{ Value string }{Value: "unknown"})
	if !errors.Is(err, transcript.ErrUnsupportedEvent) {
		t.Fatalf("error = %v, want ErrUnsupportedEvent", err)
	}
	if change.Changed() || reducer.Thread().Revision() != before.Revision() || len(reducer.Thread().Entries()) != 0 {
		t.Fatalf("unsupported event mutated transcript: %#v", change)
	}
}

func TestReducerNoOpDoesNotAdvanceRevision(t *testing.T) {
	reducer := transcript.NewReducer()
	if _, err := reducer.Apply(transcript.TurnStarted{TurnID: "turn"}); err != nil {
		t.Fatal(err)
	}
	before := reducer.Thread().Revision()
	change, err := reducer.Apply(transcript.AssistantDelta{TurnID: "turn", Delta: ""})
	if err != nil {
		t.Fatal(err)
	}
	if change.Changed() || reducer.Thread().Revision() != before || len(change.Records) != 0 {
		t.Fatalf("no-op change = %#v revision=%d want=%d", change, reducer.Thread().Revision(), before)
	}
}

func TestReducerAssignsNestedToolParentFromCanonicalState(t *testing.T) {
	reducer := transcript.NewReducer()
	parent, err := reducer.Apply(transcript.ToolStarted{TurnID: "turn", ToolID: "parent", Name: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := reducer.Apply(transcript.ToolStarted{TurnID: "turn", ToolID: "child", ParentToolID: "parent", Name: "child"})
	if err != nil {
		t.Fatal(err)
	}
	if child.Records[0].ParentID != parent.PrimaryEntryID {
		t.Fatalf("child parent = %q, want %q", child.Records[0].ParentID, parent.PrimaryEntryID)
	}
}

func TestReducerRejectsInvalidKnownEventWithoutMutation(t *testing.T) {
	reducer := transcript.NewReducer()
	change, err := reducer.Apply(transcript.ToolStarted{TurnID: "turn"})
	if !errors.Is(err, transcript.ErrInvalidEvent) {
		t.Fatalf("error = %v, want ErrInvalidEvent", err)
	}
	if change.Changed() || reducer.Thread().Revision() != 0 {
		t.Fatalf("invalid event mutated transcript: %#v", change)
	}
}
func TestReducerRejectsUnownedDiffAndPlanEvents(t *testing.T) {
	t.Parallel()

	events := []any{
		transcript.DiffAdded{Path: "x", Diff: "+x"},
		transcript.PlanUpdated{Plan: code.Plan{Steps: []code.PlanStep{{Text: "p", Status: code.StepStatuses.PENDING}}}},
	}
	for _, event := range events {
		reducer := transcript.NewReducer()
		change, err := reducer.Apply(event)
		if !errors.Is(err, transcript.ErrInvalidEvent) {
			t.Fatalf("%T error = %v, want ErrInvalidEvent", event, err)
		}
		if change.Changed() || reducer.Thread().Revision() != 0 {
			t.Fatalf("%T mutated transcript: %#v", event, change)
		}
	}
}

func TestReducerDeclaresPersistencePolicyForEveryEvent(t *testing.T) {
	events := []any{
		transcript.UserSubmitted{Text: "u"}, transcript.QueuedUserAdded{Text: "q"},
		transcript.NoticeAdded{Text: "n"}, transcript.TurnStarted{TurnID: "t"},
		transcript.AssistantDelta{TurnID: "t", Delta: "a"}, transcript.ReasoningDelta{TurnID: "t", Delta: "r"},
		transcript.SkillLoaded{TurnID: "t", Name: "s"},
		transcript.ToolStarted{TurnID: "t", ToolID: "tool", Name: "read"},
		transcript.ToolFinished{ToolID: "tool"}, transcript.DiffAdded{TurnID: "t", Path: "x", Diff: "+x"},
		transcript.PlanUpdated{TurnID: "t", Plan: code.Plan{Steps: []code.PlanStep{{Text: "p", Status: code.StepStatuses.PENDING}}}},
		transcript.SubagentReserved{SpawnToolID: "spawn", AgentName: "agent"},
		transcript.SubagentStarted{TurnID: "child", SpawnToolID: "spawn", AgentName: "agent"},
		transcript.SubagentFinished{TurnID: "child"}, transcript.TurnFinished{TurnID: "t"},
	}
	reducer := transcript.NewReducer()
	for _, event := range events {
		change, err := reducer.Apply(event)
		if err != nil {
			t.Fatalf("%T: %v", event, err)
		}
		if change.Changed() && change.Persistence == transcript.Persistences.PERSISTENCENONE {
			t.Fatalf("%T changed without persistence policy", event)
		}
	}
}
