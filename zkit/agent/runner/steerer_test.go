package runner_test

import (
	"context"
	"iter"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// twoTurnProvider emits one tool call on iteration 1 (so the runner loops)
// and a final assistant response on iteration 2 (terminating).
type twoTurnProvider struct {
	iter atomic.Int32
}

func (p *twoTurnProvider) Complete(_ context.Context, _ llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	return func(yield func(llm.CompletionChunk, error) bool) {
		switch p.iter.Add(1) {
		case 1:
			yield(llm.CompletionChunk{
				ToolCalls: []llm.ToolCall{{
					ID:       "tc1",
					Type:     "function",
					Function: llm.ToolCallFunction{Name: "noop", Arguments: "{}"},
				}},
			}, nil)
		default:
			yield(llm.CompletionChunk{Content: "done"}, nil)
		}
	}, nil
}

func (p *twoTurnProvider) Name() string { return "two-turn" }

type recordingSteerer struct {
	calls atomic.Int32
	mu    sync.Mutex
	once  []llm.Message
}

func (s *recordingSteerer) Drain(_ context.Context) iter.Seq[llm.Message] {
	var out []llm.Message
	if s.calls.Add(1) == 2 {
		s.mu.Lock()
		out = s.once
		s.once = nil
		s.mu.Unlock()
	}
	return func(yield func(llm.Message) bool) {
		for _, m := range out {
			if !yield(m) {
				return
			}
		}
	}
}

func TestRunnerDrainsSteererBetweenIterations(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	sink := newRecordingSink()

	reg := tools.NewRegistry()
	reg.Register(&noopTool{})

	steerer := &recordingSteerer{
		once: []llm.Message{{Role: "user", Content: "redirect mid-flight"}},
	}

	r := runner.New(runner.ClientFromProvider(&twoTurnProvider{}), runner.WithTools(reg),
		runner.WithSink(sink),
		runner.WithSteerer(steerer),
		runner.WithMaxIterations(4))

	res := r.Run(ctx, runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "go",
	})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if res.Reason != runner.TerminalCompleted {
		t.Fatalf("Reason: got %q, want completed", res.Reason)
	}

	if got := steerer.calls.Load(); got != 2 {
		t.Fatalf("steerer.Drain called %d times, want 2", got)
	}
	if got := sink.steerCount(); got != 1 {
		t.Fatalf("SteerInjected fired %d times, want 1", got)
	}
	injected, _ := sink.firstSteer()
	if len(injected.Messages) != 1 || injected.Messages[0].Content != "redirect mid-flight" {
		t.Fatalf("SteerInjected: got %+v, want one message", injected.Messages)
	}

	var seen bool
	for _, m := range res.Messages {
		if m.Role == "user" && m.Content == "redirect mid-flight" {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("injected user message not in final history: %+v", res.Messages)
	}
}

// noopTool is a minimal Tool implementation.
type noopTool struct{}

func (noopTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: "noop", Description: "no-op", Parameters: llm.Schema{Type: "object"}}
}
func (noopTool) Execute(_ context.Context, _ tools.ToolCall) (*tools.ToolResult, error) {
	return &tools.ToolResult{Success: true, Data: "ok"}, nil
}
