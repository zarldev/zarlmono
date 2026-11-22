package teasink_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestCompactionSettlementOwnsUsageAfterCancellation(t *testing.T) {
	send, snapshot := recordingSend()
	sink := teasink.New(send)
	t.Cleanup(sink.Close)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	usage := llm.Usage{PromptTokens: 12, ReasoningTokensReported: true}
	sink.OnCompactionSettled(ctx, runner.CompactionSettled{TaskID: "turn", Inference: compact.Inference{Provider: "other", Model: "small", Usage: &usage}, Err: context.Canceled})
	usage.PromptTokens = 99
	sink.Drain()
	messages := snapshot()
	if len(messages) != 1 {
		t.Fatalf("messages = %v", messages)
	}
	got, ok := messages[0].(teasink.CompactionSettledMsg)
	if !ok || got.Inference.Usage.PromptTokens != 12 || !got.Inference.Usage.ReasoningTokensReported || !errors.Is(got.Err, context.Canceled) {
		t.Fatalf("settlement = %+v", messages[0])
	}
}

func TestCompactionSettlementOwnsSidecarsAfterCancellation(t *testing.T) {
	t.Parallel()
	send, snapshot := recordingSend()
	sink := teasink.New(send)
	t.Cleanup(sink.Close)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	inference := compact.Inference{Provider: "aux-provider", Model: "small", Duration: time.Second,
		Usage: &llm.Usage{PromptTokens: 12, ReasoningTokensReported: true},
		PricingUsage: &llm.PricingUsage{Input: llm.PricingInputUsage{Total: llm.TokenQuantity{Known: true},
			WritesByLifetime: []llm.CacheWriteQuantity{{Lifetime: time.Hour, Quantity: llm.TokenQuantity{Tokens: 4, Known: true}}}}},
		Execution: &llm.ExecutionObservation{RequestedModel: "aux-alias", GatewayDeployment: "aux-deployment", ReportedCost: llm.ReportedCost{Reported: true}}}
	want := inference.Clone()
	sink.OnContent(ctx, runner.Content{TaskID: "turn", Delta: "before compaction"})
	sink.OnCompactionSettled(ctx, runner.CompactionSettled{TaskID: "turn", Depth: 2, Inference: inference, Err: context.Canceled})
	inference.Usage.PromptTokens = 99
	inference.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 99
	inference.Execution.GatewayDeployment = "source-mutated"
	inference.Execution.ReportedCost.Currency = "USD"
	sink.OnCompactionSettled(ctx, runner.CompactionSettled{TaskID: "turn"})
	sink.OnCompactionSettled(ctx, runner.CompactionSettled{TaskID: "turn", Inference: compact.Inference{PricingUsage: &llm.PricingUsage{}, Execution: &llm.ExecutionObservation{}}})
	sink.Drain()
	messages := snapshot()
	if len(messages) != 4 {
		t.Fatalf("messages = %+v", messages)
	}
	if content, ok := messages[0].(teasink.ContentMsg); !ok || content.Delta != "before compaction" {
		t.Fatalf("preceding content = %+v", messages[0])
	}
	got, ok := messages[1].(teasink.CompactionSettledMsg)
	if !ok || got.TaskID != "turn" || got.Depth != 2 || !errors.Is(got.Err, context.Canceled) || !reflect.DeepEqual(got.Inference, want) {
		t.Fatalf("settlement = %+v", messages[1])
	}
	got.Inference.Usage.PromptTokens = 88
	got.Inference.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 88
	got.Inference.Execution.GatewayDeployment = "consumer-mutated"
	if inference.Usage.PromptTokens != 99 || inference.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens != 99 || inference.Execution.GatewayDeployment != "source-mutated" {
		t.Fatal("consumer mutation escaped to borrowed source")
	}
	absent := messages[2].(teasink.CompactionSettledMsg).Inference
	unknown := messages[3].(teasink.CompactionSettledMsg).Inference
	if absent.Usage != nil || absent.PricingUsage != nil || absent.Execution != nil || unknown.Usage != nil || unknown.PricingUsage == nil || unknown.Execution == nil || unknown.Execution.ReportedCost.Reported || unknown.PricingUsage.Input.Total.Known {
		t.Fatalf("absent = %+v; unknown = %+v", absent, unknown)
	}
}
