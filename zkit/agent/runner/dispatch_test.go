package runner_test

import (
	"context"
	"fmt"
	"iter"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// batchProvider emits N tool calls in a single assistant message on
// iteration 1, then a final assistant response on iteration 2.
type batchProvider struct {
	iter       atomic.Int32
	toolName   string
	batchSize  int
	finalReply string
}

func (p *batchProvider) Complete(_ context.Context, _ llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	return func(yield func(llm.CompletionChunk, error) bool) {
		switch p.iter.Add(1) {
		case 1:
			calls := make([]llm.ToolCall, 0, p.batchSize)
			for i := range p.batchSize {
				calls = append(calls, llm.ToolCall{
					ID:   fmt.Sprintf("tc-%d", i),
					Type: "function",
					Function: llm.ToolCallFunction{
						Name:      p.toolName,
						Arguments: fmt.Sprintf(`{"i":%d}`, i),
					},
				})
			}
			yield(llm.CompletionChunk{ToolCalls: calls}, nil)
		default:
			yield(llm.CompletionChunk{Content: p.finalReply}, nil)
		}
	}, nil
}

// concurrencyTrackingTool sleeps briefly to amplify concurrency, and
// records the peak number of simultaneous in-flight executions so the
// test can assert real parallelism (not just an absence of crashes).
type concurrencyTrackingTool struct {
	name    string
	mu      sync.Mutex
	active  int32
	peak    int32
	delay   time.Duration
	results map[string]struct{} // tracks observed call IDs
}

func newConcurrencyTrackingTool(_ string, delay time.Duration) *concurrencyTrackingTool {
	return &concurrencyTrackingTool{
		name:    "track",
		delay:   delay,
		results: make(map[string]struct{}),
	}
}

func (t *concurrencyTrackingTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        tools.ToolName(t.name),
		Description: "concurrency-tracking test tool",
		Parameters:  llm.Schema{Type: "object"},
	}
}

func (t *concurrencyTrackingTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	cur := atomic.AddInt32(&t.active, 1)
	defer atomic.AddInt32(&t.active, -1)
	for {
		old := atomic.LoadInt32(&t.peak)
		if cur <= old || atomic.CompareAndSwapInt32(&t.peak, old, cur) {
			break
		}
	}
	time.Sleep(t.delay)
	t.mu.Lock()
	t.results[call.ID] = struct{}{}
	t.mu.Unlock()
	return &tools.ToolResult{Success: true, Data: call.ID}, nil
}

func (t *concurrencyTrackingTool) Peak() int32 {
	return atomic.LoadInt32(&t.peak)
}

func TestRunnerDispatchesParallelRegistryTools(t *testing.T) {
	t.Parallel()

	tt := newConcurrencyTrackingTool("track", 60*time.Millisecond)
	reg := tools.NewRegistry()
	reg.Register(tt)

	const limit = 3
	const batch = 6

	r := runner.New(
		runner.ClientFromProvider(&batchProvider{toolName: "track", batchSize: batch, finalReply: "ok"}),
		runner.WithTools(reg),
		runner.WithMaxIterations(4),
		runner.WithToolConcurrency(limit),
	)

	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "go",
	})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if res.Reason != runner.TerminalCompleted {
		t.Fatalf("got Reason=%q, want completed", res.Reason)
	}

	peak := tt.Peak()
	if peak < 2 {
		t.Errorf("peak concurrency=%d, want >= 2 (parallelism not happening)", peak)
	}
	if peak > limit {
		t.Errorf("peak concurrency=%d exceeded limit=%d", peak, limit)
	}
}

func TestRunnerDispatchesSequentiallyByDefault(t *testing.T) {
	t.Parallel()

	tt := newConcurrencyTrackingTool("track", 30*time.Millisecond)
	reg := tools.NewRegistry()
	reg.Register(tt)

	r := runner.New(
		runner.ClientFromProvider(&batchProvider{toolName: "track", batchSize: 4, finalReply: "ok"}),
		runner.WithTools(reg),
		runner.WithMaxIterations(4),
		// No WithToolConcurrency — historical sequential behaviour.
	)

	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "go",
	})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if res.Reason != runner.TerminalCompleted {
		t.Fatalf("got Reason=%q, want completed", res.Reason)
	}

	if peak := tt.Peak(); peak != 1 {
		t.Errorf("peak concurrency=%d, want 1 (default should be sequential)", peak)
	}
}

func TestRunnerPreservesToolCallOrderUnderParallelDispatch(t *testing.T) {
	t.Parallel()

	tt := newConcurrencyTrackingTool("track", 20*time.Millisecond)
	reg := tools.NewRegistry()
	reg.Register(tt)

	r := runner.New(
		runner.ClientFromProvider(&batchProvider{toolName: "track", batchSize: 5, finalReply: "ok"}),
		runner.WithTools(reg),
		runner.WithMaxIterations(4),
		runner.WithToolConcurrency(5),
	)

	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "go",
	})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}

	// Pull the tool messages from the final history. Their ToolCallID
	// values must appear in tc-0 .. tc-4 order regardless of which
	// goroutine finished first — that's the contract.
	var observedIDs []string
	for _, m := range res.Messages {
		if m.Role == "tool" {
			observedIDs = append(observedIDs, m.ToolCallID)
		}
	}
	want := []string{"tc-0", "tc-1", "tc-2", "tc-3", "tc-4"}
	if len(observedIDs) != len(want) {
		t.Fatalf("observed %d tool messages, want %d", len(observedIDs), len(want))
	}
	for i := range want {
		if observedIDs[i] != want[i] {
			t.Errorf(
				"position %d: got %q, want %q (parallel dispatch must preserve toolCallOrder)",
				i,
				observedIDs[i],
				want[i],
			)
		}
	}
}

// failingTool always returns ErrInternal — used to verify that one
// failing tool in a parallel batch does NOT cancel siblings.
type failingTool struct{ name string }

func (f failingTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        tools.ToolName(f.name),
		Description: "always fails",
		Parameters:  llm.Schema{Type: "object"},
	}
}
func (f failingTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	return &tools.ToolResult{ToolCallID: call.ID, Success: false, Error: "boom"}, nil
}

func TestRunnerParallelBatchDoesNotCancelSiblingsOnFailure(t *testing.T) {
	t.Parallel()

	tt := newConcurrencyTrackingTool("track", 30*time.Millisecond)
	reg := tools.NewRegistry()
	reg.Register(tt)
	reg.Register(failingTool{name: "fail"})

	// Custom provider that emits a mix: track, fail, track, track.
	prov := &mixedBatchProvider{}

	r := runner.New(runner.ClientFromProvider(prov), runner.WithTools(reg), runner.WithMaxIterations(4), runner.WithToolConcurrency(4))
	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "go",
	})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if res.Reason != runner.TerminalCompleted {
		t.Fatalf("got Reason=%q, want completed (failing siblings must not abort the batch)", res.Reason)
	}

	tt.mu.Lock()
	defer tt.mu.Unlock()
	if len(tt.results) != 3 {
		t.Errorf("track tool ran %d times, want 3 (the failing sibling cancelled the batch)", len(tt.results))
	}
}

func (p *batchProvider) Name() string { return "batch" }

type mixedBatchProvider struct{ iter atomic.Int32 }

func (p *mixedBatchProvider) Name() string { return "mixed" }

func (p *mixedBatchProvider) Complete(_ context.Context, _ llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	return func(yield func(llm.CompletionChunk, error) bool) {
		switch p.iter.Add(1) {
		case 1:
			yield(llm.CompletionChunk{
				ToolCalls: []llm.ToolCall{
					{ID: "tc-0", Type: "function", Function: llm.ToolCallFunction{Name: "track", Arguments: "{}"}},
					{ID: "tc-1", Type: "function", Function: llm.ToolCallFunction{Name: "fail", Arguments: "{}"}},
					{ID: "tc-2", Type: "function", Function: llm.ToolCallFunction{Name: "track", Arguments: "{}"}},
					{ID: "tc-3", Type: "function", Function: llm.ToolCallFunction{Name: "track", Arguments: "{}"}},
				},
			}, nil)
		default:
			yield(llm.CompletionChunk{Content: "done"}, nil)
		}
	}, nil
}
