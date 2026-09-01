package engine_test

import (
	"context"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/agent/diffrecorder"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

type streamingProvider struct{}

func (streamingProvider) Name() string { return "streaming" }

func (streamingProvider) Complete(context.Context, llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		if !yield(llm.CompletionChunk{Content: "first "}, nil) {
			return
		}
		yield(llm.CompletionChunk{Content: "second"}, nil)
	}
}

type liveRecordingSink struct {
	runner.NopSink
	mu      sync.Mutex
	content []string
}

func (s *liveRecordingSink) OnContent(event runner.Content) {
	s.mu.Lock()
	s.content = append(s.content, event.Delta)
	s.mu.Unlock()
}

func (*liveRecordingSink) DiffEvent(diffrecorder.DiffEvent) {}
func (*liveRecordingSink) PlanUpdated(code.Plan)            {}

func (s *liveRecordingSink) Content() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.content...)
}

func TestLiveRunnerStreamsContent(t *testing.T) {
	t.Parallel()

	workspace, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	sink := &liveRecordingSink{}
	live := engine.NewLiveRunner(streamingProvider{}, workspace, "model", engine.WithLiveSink(sink))
	if err := live.RunTurn(t.Context(), "respond"); err != nil {
		t.Fatalf("run turn: %v", err)
	}

	got := sink.Content()
	if len(got) != 2 || got[0] != "first " || got[1] != "second" {
		t.Fatalf("streamed content = %q, want [first_ second]", got)
	}
	history := live.History()
	if len(history) != 2 || history[1].Content != "first second" {
		t.Fatalf("history = %#v, want accumulated assistant response", history)
	}
}
