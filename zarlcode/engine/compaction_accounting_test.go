package engine_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

type manualAccountingSink struct {
	runner.NopSink
	events               []runner.CompactionSettled
	applied              []runner.CompactionApplied
	settlementContextErr error
}

func (s *manualAccountingSink) OnCompactionSettled(ctx context.Context, e runner.CompactionSettled) {
	s.settlementContextErr = ctx.Err()
	s.events = append(s.events, e)
}

func TestManualCompactionSettlesFailedInference(t *testing.T) {
	var cache engine.ContextCache
	cache.Restore([]llm.Message{{Role: llm.RoleUser, Content: "keep this"}})
	failure := errors.New("provider interrupted")
	sink := &manualAccountingSink{}
	_, err := cache.Compact(t.Context(), compact.Func(func(context.Context, []llm.Message, int) (compact.Result, error) {
		return compact.Result{Inference: &compact.Inference{Provider: "other", Model: "small", Usage: &llm.Usage{PromptTokens: 10}}}, failure
	}), sink)
	if !errors.Is(err, failure) || len(sink.events) != 1 || !errors.Is(sink.events[0].Err, failure) {
		t.Fatalf("err=%v events=%+v", err, sink.events)
	}
	if sink.events[0].TaskID != "manual-compact" || sink.events[0].Inference.Usage.PromptTokens != 10 {
		t.Fatalf("event=%+v", sink.events[0])
	}
	if cache.Snapshot()[0].Content != "keep this" {
		t.Fatal("failed compaction changed history")
	}
}

func (s *manualAccountingSink) OnCompactionApplied(_ context.Context, e runner.CompactionApplied) {
	s.applied = append(s.applied, e)
}

func TestManualCompactionObservationOwnershipAndContext(t *testing.T) {
	t.Parallel()
	failure := errors.New("auxiliary provider interrupted")
	for _, tt := range []struct {
		name   string
		err    error
		cancel bool
	}{
		{name: "success"},
		{name: "failure", err: failure},
		{name: "canceled", err: context.Canceled, cancel: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var cache engine.ContextCache
			original := []llm.Message{{Role: llm.RoleUser, Content: "keep this"}, {Role: llm.RoleAssistant, Content: "and this"}}
			cache.Restore(original)
			sink := &manualAccountingSink{}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			inference := compact.Inference{Provider: "aux-provider", Model: "small", Duration: time.Second,
				Usage:        &llm.Usage{PromptTokens: 10, ReasoningTokensReported: true},
				PricingUsage: &llm.PricingUsage{Input: llm.PricingInputUsage{WritesByLifetime: []llm.CacheWriteQuantity{{Lifetime: time.Hour, Quantity: llm.TokenQuantity{Known: true}}}}},
				Execution:    &llm.ExecutionObservation{GatewayDeployment: "aux-deployment", ReportedCost: llm.ReportedCost{Reported: true}}}
			want := inference.Clone()
			history := []llm.Message{{Role: llm.RoleUser, Content: "compacted"}}
			result, err := cache.Compact(ctx, compact.Func(func(_ context.Context, borrowed []llm.Message, _ int) (compact.Result, error) {
				borrowed[0].Content = "compactor mutated its input"
				if tt.cancel {
					cancel()
				}
				return compact.Result{History: history, Inference: &inference, Engine: compact.EngineSummary, BytesTrimmed: 10}, tt.err
			}), sink)
			if !errors.Is(err, tt.err) || len(sink.events) != 1 || !errors.Is(sink.events[0].Err, tt.err) {
				t.Fatalf("error = %v; settlements = %+v", err, sink.events)
			}
			if tt.cancel && !errors.Is(sink.settlementContextErr, context.Canceled) {
				t.Fatal("canceled settlement context was lost")
			}
			if sink.events[0].TaskID != "manual-compact" || !reflect.DeepEqual(sink.events[0].Inference, want) {
				t.Fatalf("settlement = %+v; want %+v", sink.events[0], want)
			}
			inference.Usage.PromptTokens = 900
			inference.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 900
			inference.Execution.GatewayDeployment = "source-mutated"
			inference.Execution.ReportedCost.Currency = "USD"
			if !reflect.DeepEqual(sink.events[0].Inference, want) {
				t.Fatal("settlement aliases compactor inference")
			}
			sink.events[0].Inference.Usage.PromptTokens = 800
			sink.events[0].Inference.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens = 800
			sink.events[0].Inference.Execution.GatewayDeployment = "sink-mutated"
			if inference.Usage.PromptTokens != 900 || inference.PricingUsage.Input.WritesByLifetime[0].Quantity.Tokens != 900 || inference.Execution.GatewayDeployment != "source-mutated" {
				t.Fatal("sink mutation escaped to compactor")
			}
			if tt.err != nil {
				if !reflect.DeepEqual(cache.Snapshot(), original) || len(sink.applied) != 0 {
					t.Fatal("unsuccessful compaction changed context or published applied")
				}
				return
			}
			if !reflect.DeepEqual(cache.Snapshot(), history) || len(sink.applied) != 1 || result.MessagesBefore != 2 || result.MessagesAfter != 1 || result.BytesTrimmed != 10 {
				t.Fatalf("successful compaction = %+v; applied = %+v; context = %+v", result, sink.applied, cache.Snapshot())
			}
			history[0].Content = "source-mutated"
			if cache.Snapshot()[0].Content != "compacted" {
				t.Fatal("compacted history aliases source")
			}
		})
	}
}
