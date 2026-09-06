package runner_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

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

func (p *batchProvider) Complete(_ context.Context, _ llm.CompletionRequest) llm.CompletionStream {
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
	}
}

// concurrencyTrackingTool sleeps briefly to amplify concurrency, and
// records the peak number of simultaneous in-flight executions so the
// test can assert real parallelism (not just an absence of crashes).
type concurrencyTrackingTool struct {
	name    string
	mu      sync.Mutex
	active  int32
	peak    int32
	results map[string]struct{}
	started chan<- struct{}
	release <-chan struct{}
}

func newConcurrencyTrackingTool(_ string) *concurrencyTrackingTool {
	return &concurrencyTrackingTool{
		name:    "track",
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
	if t.started != nil {
		t.started <- struct{}{}
		<-t.release
	}
	t.mu.Lock()
	t.results[call.ID.String()] = struct{}{}
	t.mu.Unlock()
	return &tools.ToolResult{Success: true, Data: call.ID}, nil
}

func (t *concurrencyTrackingTool) Peak() int32 {
	return atomic.LoadInt32(&t.peak)
}

func TestRunnerDispatchesParallelRegistryTools(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := make(chan struct{}, 3)
		release := make(chan struct{})
		tt := newConcurrencyTrackingTool("track")
		tt.started = started
		tt.release = release
		reg := tools.NewRegistry()
		reg.Register(tt)

		const limit = 3
		r := runner.New(
			runner.ClientFromProvider(&batchProvider{toolName: "track", batchSize: 6, finalReply: "ok"}),
			runner.WithTools(reg),
			runner.WithMaxIterations(4),
			runner.WithToolConcurrency(limit),
		)
		done := make(chan runner.TaskResult, 1)
		go func() { done <- r.Run(t.Context(), runner.TaskSpec{ID: taskscope.ID(uuid.NewString()), Prompt: "go"}) }()
		for range limit {
			<-started
		}
		close(release)
		res := <-done
		if res.Err != nil {
			t.Fatalf("Run: %v", res.Err)
		}
		if res.Reason != runner.TerminalCompleted {
			t.Fatalf("got Reason=%q, want completed", res.Reason)
		}
		if peak := tt.Peak(); peak != limit {
			t.Errorf("peak concurrency=%d, want %d", peak, limit)
		}
	})
}

func TestRunnerDispatchesSequentiallyByDefault(t *testing.T) {
	tt := newConcurrencyTrackingTool("track")
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
	tt := newConcurrencyTrackingTool("track")
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

type reusedIDProvider struct{ iter atomic.Int32 }

func (p *reusedIDProvider) Complete(_ context.Context, _ llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		if p.iter.Add(1) == 1 {
			yield(llm.CompletionChunk{ToolCalls: []llm.ToolCall{
				{ID: "reused", Type: "function", OutputIndex: llm.OutputPosition(0), Function: llm.ToolCallFunction{Name: "identity", Arguments: `{"label":"A"}`}},
				{ID: "reused", Type: "function", OutputIndex: llm.OutputPosition(1), Function: llm.ToolCallFunction{Name: "identity", Arguments: `{"label":"B"}`}},
			}}, nil)
			return
		}
		yield(llm.CompletionChunk{Content: "done"}, nil)
	}
}

type executionStart struct {
	label       string
	executionID string
}

type identityTool struct {
	started  chan<- executionStart
	releaseA <-chan struct{}
	releaseB <-chan struct{}
}

func (identityTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: "identity", Description: "records execution identity", Parameters: llm.Schema{Type: "object"}}
}

func (tool identityTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	label, _ := call.Arguments["label"].(string)
	tool.started <- executionStart{label: label, executionID: call.ExecutionID}
	if label == "A" {
		<-tool.releaseA
	} else {
		<-tool.releaseB
	}
	return &tools.ToolResult{ToolCallID: call.ID, Success: true, Data: label}, nil
}

type identitySink struct {
	runner.NopSink
	started   chan runner.ToolStarted
	completed chan runner.ToolCompleted
}

func (sink *identitySink) OnToolStarted(event runner.ToolStarted)     { sink.started <- event }
func (sink *identitySink) OnToolCompleted(event runner.ToolCompleted) { sink.completed <- event }

func TestRunnerUsesExactExecutionIdentityForReusedProviderToolCallID(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := make(chan executionStart, 2)
		releaseA := make(chan struct{})
		releaseB := make(chan struct{})
		tool := identityTool{started: started, releaseA: releaseA, releaseB: releaseB}
		reg := tools.NewRegistry()
		reg.Register(tool)
		sink := &identitySink{started: make(chan runner.ToolStarted, 2), completed: make(chan runner.ToolCompleted, 2)}
		r := runner.New(
			&reusedIDProvider{},
			runner.WithTools(reg),
			runner.WithSink(sink),
			runner.WithToolConcurrency(2),
			runner.WithMaxIterations(3),
		)

		ctx := t.Context()
		done := make(chan runner.TaskResult, 1)
		go func() {
			done <- r.Run(ctx, runner.TaskSpec{ID: taskscope.ID(uuid.NewString()), Prompt: "go"})
		}()
		synctest.Wait()
		if len(started) != 2 || len(sink.started) != 2 {
			close(releaseA)
			close(releaseB)
			synctest.Wait()
			t.Fatalf("start observations: tool=%d events=%d, want 2 each", len(started), len(sink.started))
		}

		executions := make(map[string]string, 2)
		for range 2 {
			start := <-started
			executions[start.label] = start.executionID
		}
		if executions["A"] == "" || executions["B"] == "" || executions["A"] == executions["B"] {
			t.Errorf("tool execution IDs = %#v, want distinct non-empty identities", executions)
		}

		startEvents := make(map[string]string, 2)
		for range 2 {
			event := <-sink.started
			label, _ := event.Parameters["label"].(string)
			startEvents[label] = event.ExecutionID
		}

		close(releaseB)
		synctest.Wait()
		if len(sink.completed) != 1 {
			close(releaseA)
			synctest.Wait()
			t.Fatalf("completions after releasing B = %d, want 1", len(sink.completed))
		}
		completedB := <-sink.completed
		close(releaseA)
		synctest.Wait()
		if len(sink.completed) != 1 || len(done) != 1 {
			t.Fatalf("final observations: completions=%d runs=%d, want 1 each", len(sink.completed), len(done))
		}
		completedA := <-sink.completed
		result := <-done
		if result.Err != nil {
			t.Fatalf("Run: %v", result.Err)
		}

		if completedB.Result != "B" || completedA.Result != "A" {
			t.Fatalf("completion order = (%v, %v), want B before A", completedB.Result, completedA.Result)
		}
		for label, completed := range map[string]runner.ToolCompleted{"A": completedA, "B": completedB} {
			if startEvents[label] != executions[label] || completed.ExecutionID != executions[label] {
				t.Errorf("%s execution IDs: tool=%q start=%q completion=%q", label, executions[label], startEvents[label], completed.ExecutionID)
			}
			if completed.ToolID != "reused" {
				t.Errorf("%s completion ToolID = %q, want reused", label, completed.ToolID)
			}
		}
	})
}

type ambiguityCountingTool struct{ calls atomic.Int32 }

func (*ambiguityCountingTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: "count", Description: "counts executions", Parameters: llm.Schema{Type: "object"}}
}

func (tool *ambiguityCountingTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	tool.calls.Add(1)
	return &tools.ToolResult{ToolCallID: call.ID, Success: true}, nil
}

type ambiguousIDProvider struct{}

func (ambiguousIDProvider) Complete(_ context.Context, _ llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		yield(llm.CompletionChunk{ToolCalls: []llm.ToolCall{
			{ID: "duplicate", Type: "function", Function: llm.ToolCallFunction{Name: "count", Arguments: "{}"}},
			{ID: "duplicate", Type: "function", Function: llm.ToolCallFunction{Name: "count", Arguments: "{}"}},
		}}, nil)
	}
}

func TestRunnerRejectsAmbiguousDuplicateToolCallIDBeforeExecution(t *testing.T) {
	client := ambiguousIDProvider{}
	tool := &ambiguityCountingTool{}
	reg := tools.NewRegistry()
	reg.Register(tool)
	r := runner.New(client, runner.WithTools(reg))

	result := r.Run(t.Context(), runner.TaskSpec{ID: taskscope.ID(uuid.NewString()), Prompt: "go"})
	if !errors.Is(result.Err, runner.ErrAmbiguousToolCalls) {
		t.Fatalf("Run error = %v, want ErrAmbiguousToolCalls", result.Err)
	}
	if got := tool.calls.Load(); got != 0 {
		t.Fatalf("tool executed %d times, want 0", got)
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
	tt := newConcurrencyTrackingTool("track")
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

func (p *mixedBatchProvider) Complete(_ context.Context, _ llm.CompletionRequest) llm.CompletionStream {
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
	}
}
