package runner_test

import (
	"context"
	"iter"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type recordingToolSpecProvider struct {
	mu       sync.Mutex
	requests []llm.CompletionRequest
}

func (p *recordingToolSpecProvider) Complete(_ context.Context, req llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.mu.Unlock()

	return func(yield func(llm.CompletionChunk, error) bool) {
		if !yield(llm.CompletionChunk{Content: "done"}, nil) {
			return
		}
		if !yield(llm.CompletionChunk{Done: true}, nil) {
			return
		}
	}, nil
}

func (p *recordingToolSpecProvider) Name() string { return "recording" }

func (p *recordingToolSpecProvider) snapshotRequests() []llm.CompletionRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]llm.CompletionRequest(nil), p.requests...)
}

func TestRunnerUsesRegistryDescriptionOverridesForLLMTools(t *testing.T) {
	store := tools.NewMemoryDescriptionStore()
	reg := tools.NewRegistry()
	reg.SetDescriptionStore(store)
	reg.Register(stubTool{name: "foo", desc: "default description"})
	store.Set("foo", "override description")

	provider := &recordingToolSpecProvider{}
	r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(reg))
	if res := r.Run(t.Context(), runner.TaskSpec{Prompt: "hello"}); res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}

	requests := provider.snapshotRequests()
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}
	if len(requests[0].Tools) != 1 {
		t.Fatalf("request tools = %d, want 1", len(requests[0].Tools))
	}
	if got := requests[0].Tools[0].Function.Description; got != "override description" {
		t.Fatalf("tool description = %q, want override description", got)
	}
}
