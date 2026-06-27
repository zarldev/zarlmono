package hitl_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/hitl"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestFormatReviewMessage(t *testing.T) {
	msg := hitl.FormatReviewMessage(hitl.Request{
		ID:           "req-1",
		RunID:        "run-1",
		CheckpointID: "cp-1",
		Action:       "edit_file",
		Summary:      "Change config loader",
		Risk:         hitl.RiskMedium,
		Payload:      map[string]any{"path": "config.go"},
	}, hitl.Review{
		RequestID: "req-1",
		Decision:  hitl.DecisionEdit,
		Reviewer:  "bruno",
		Comment:   "Preserve defaults.",
		Patch:     map[string]any{"mode": "minimal"},
	})
	for _, want := range []string{
		"[human review]",
		"request_id=req-1",
		"decision=edit",
		"reviewer=bruno",
		"checkpoint_id=cp-1",
		"action=edit_file",
		`patch={"mode":"minimal"}`,
		`payload={"path":"config.go"}`,
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message missing %q:\n%s", want, msg)
		}
	}
}

func TestSteerQueueAppendReviewDrain(t *testing.T) {
	var q hitl.SteerQueue
	if depth := q.AppendReview(hitl.Request{ID: "req"}, hitl.Review{RequestID: "req", Decision: hitl.DecisionApprove}); depth != 1 {
		t.Fatalf("depth = %d", depth)
	}
	var got []llm.Message
	for msg := range q.Drain(t.Context()) {
		got = append(got, msg)
	}
	if len(got) != 1 {
		t.Fatalf("got %d messages", len(got))
	}
	if got[0].Role != llm.RoleUser || !strings.Contains(got[0].Content, "decision=approve") {
		t.Fatalf("message = %#v", got[0])
	}
	if q.Len() != 0 {
		t.Fatalf("queue len after drain = %d", q.Len())
	}
}
