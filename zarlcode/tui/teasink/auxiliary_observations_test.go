package teasink_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestAuxiliaryObservedOrderedOwnedAfterCancellation(t *testing.T) {
	send, snapshot := recordingSend()
	sink := teasink.New(send)
	t.Cleanup(sink.Close)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	o := engine.AuxiliaryObservation{Kind: engine.AuxiliaryKinds.SPAWNPLANNER, ResponseValid: true, Inference: inference.Observation{TaskID: "owner", Depth: 2, Provider: "actual", Model: "actual-model", Outcome: inference.StreamOutcomes.COMPLETED, Usage: &llm.Usage{}, PricingUsage: &llm.PricingUsage{Input: llm.PricingInputUsage{WritesByLifetime: []llm.CacheWriteQuantity{{Lifetime: time.Hour, Quantity: llm.TokenQuantity{Known: true}}}}}, Execution: &llm.ExecutionObservation{ReportedCost: llm.ReportedCost{Reported: true}}}}
	want := o.Clone()
	sink.OnContent(ctx, runner.Content{TaskID: "owner", Delta: "before"})
	sink.AuxiliaryObserved(ctx, o)
	o.Inference.Usage.PromptTokens = 9000
	o.Inference.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 9000
	o.Inference.Execution.ReportedCost.Amount = 9000
	sink.OnConversationEnded(ctx, runner.ConversationEnded{TaskID: "owner", Reason: runner.TerminalCompleted})
	sink.Drain()
	msgs := snapshot()
	if len(msgs) != 3 {
		t.Fatalf("messages=%+v", msgs)
	}
	if text, ok := msgs[0].(teasink.ContentMsg); !ok || text.Delta != "before" {
		t.Fatalf("preceding text=%+v", msgs[0])
	}
	got, ok := msgs[1].(teasink.AuxiliaryObservedMsg)
	if !ok || !reflect.DeepEqual(got.Observation, want) {
		t.Fatalf("queued observation=%+v", msgs[1])
	}
	if _, ok := msgs[2].(teasink.ConversationEndedMsg); !ok {
		t.Fatalf("following terminal=%+v", msgs[2])
	}
}
