package teasink_test

import (
	"context"
	"errors"
	"testing"

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
