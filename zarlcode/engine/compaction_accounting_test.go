package engine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

type manualAccountingSink struct {
	runner.NopSink
	events []runner.CompactionSettled
}

func (s *manualAccountingSink) OnCompactionSettled(_ context.Context, e runner.CompactionSettled) {
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
