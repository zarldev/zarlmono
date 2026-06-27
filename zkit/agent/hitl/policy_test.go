package hitl_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/hitl"
)

func TestApproveLowRisk(t *testing.T) {
	review, ok, err := hitl.ApproveLowRisk{}.Review(t.Context(), hitl.Request{ID: "r", Risk: hitl.RiskLow})
	if err != nil {
		t.Fatal(err)
	}
	if !ok || review.Decision != hitl.DecisionApprove {
		t.Fatalf("review=%#v ok=%v", review, ok)
	}
	_, ok, err = hitl.ApproveLowRisk{}.Review(t.Context(), hitl.Request{ID: "r", Risk: hitl.RiskHigh})
	if err != nil || ok {
		t.Fatalf("high risk ok=%v err=%v", ok, err)
	}
}
