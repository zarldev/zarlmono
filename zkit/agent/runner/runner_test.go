package runner_test

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// fakeProvider scripts a sequence of CompletionChunk arrays — one
// array per Complete() call, in the order the runner makes them.
type fakeProvider struct {
	mu       sync.Mutex
	turns    [][]llm.CompletionChunk
	calls    int
	requests []llm.CompletionRequest
}

func (f *fakeProvider) Complete(_ context.Context, req llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	if f.calls >= len(f.turns) {
		return nil, fmt.Errorf("fakeProvider: out of scripted turns (call #%d)", f.calls+1)
	}
	chunks := f.turns[f.calls]
	f.calls++
	return func(yield func(llm.CompletionChunk, error) bool) {
		for _, c := range chunks {
			err := c.Error
			c.Error = nil
			if !yield(c, err) {
				return
			}
		}
	}, nil
}

func (f *fakeProvider) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeProvider) request(i int) llm.CompletionRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[i]
}

func (f *fakeProvider) Name() string { return "fake" }

// stubTool matches an expected tool call, returns a canned result.
type stubTool struct {
	name    tools.ToolName
	desc    string
	result  string
	effects []tools.Effect
}

func (s stubTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: s.name, Description: s.desc}
}
func (s stubTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	return &tools.ToolResult{
		ToolCallID: call.ID,
		Success:    true,
		Data:       s.result,
		Effects:    s.effects,
		ExecutedAt: time.Now(),
	}, nil
}

func newRegistry(stubs ...tools.Tool) *tools.Registry {
	reg := tools.NewRegistry()
	for _, s := range stubs {
		reg.Register(s)
	}
	return reg
}

func chunkText(text string) llm.CompletionChunk {
	return llm.CompletionChunk{Content: text, Done: false}
}

func chunkToolCall(id, name, args string) llm.CompletionChunk {
	return llm.CompletionChunk{
		ToolCalls: []llm.ToolCall{
			{
				ID:   id,
				Type: "function",
				Function: llm.ToolCallFunction{
					Name:      name,
					Arguments: args,
				},
			},
		},
	}
}

func chunkDone() llm.CompletionChunk {
	return llm.CompletionChunk{Done: true}
}

func chunkDoneUsage(prompt, completion int) llm.CompletionChunk {
	return llm.CompletionChunk{
		Done: true,
		Usage: &llm.Usage{
			PromptTokens:     prompt,
			CompletionTokens: completion,
			TotalTokens:      prompt + completion,
		},
	}
}

// --- tests ---

func TestRun_SkipsToolsWithEmptyNames(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		turns: [][]llm.CompletionChunk{
			{chunkText("ok"), chunkDone()},
		},
	}
	reg := newRegistry(
		stubTool{name: "", result: "bad"},
		stubTool{name: "read", result: "good"},
	)

	r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(reg), runner.WithMaxIterations(1))
	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "hello",
	})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}

	req := provider.request(0)
	if len(req.Tools) != 1 {
		t.Fatalf("tool count = %d, want 1 (%+v)", len(req.Tools), req.Tools)
	}
	if got := req.Tools[0].Function.Name; got != "read" {
		t.Fatalf("tool name = %q, want read", got)
	}
}

func TestRun_TextOnlyResponseEndsAsCompleted(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		turns: [][]llm.CompletionChunk{
			{chunkText("here is the answer"), chunkDoneUsage(42, 5)},
		},
	}

	reg := newRegistry()

	sink := newRecordingSink()
	r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(reg), runner.WithMaxIterations(3), runner.WithSink(sink), runner.WithContextBreakdown())
	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "what is the answer",
	})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if res.Reason != runner.TerminalCompleted {
		t.Errorf("reason = %v, want completed", res.Reason)
	}
	if res.FinalContent != "here is the answer" {
		t.Errorf("FinalContent = %q", res.FinalContent)
	}
	iterations := sink.iterationEvents()
	if len(iterations) != 1 {
		t.Fatalf("iteration events = %d, want 1", len(iterations))
	}
	if got := iterations[0].Usage; got == nil || got.PromptTokens != 42 || got.CompletionTokens != 5 {
		t.Fatalf("iteration usage = %+v, want prompt=42 completion=5", got)
	}
	if iterations[0].Context == nil || iterations[0].Context.UserMsgs != 1 || iterations[0].Context.AssistantMsgs != 1 {
		t.Fatalf("iteration context = %+v, want user+assistant breakdown", iterations[0].Context)
	}
}

// TestRun_ContextBreakdownOffByDefault locks the gating: without
// WithContextBreakdown the IterationCompleted event still carries iter +
// usage, but Context is nil so a headless/eval run skips the per-role walk.
func TestRun_ContextBreakdownOffByDefault(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		turns: [][]llm.CompletionChunk{
			{chunkText("answer"), chunkDoneUsage(10, 2)},
		},
	}
	sink := newRecordingSink()
	r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(newRegistry()),
		runner.WithMaxIterations(3), runner.WithSink(sink))
	if res := r.Run(t.Context(), runner.TaskSpec{
		ID: taskscope.ID(uuid.NewString()), Prompt: "q",
	}); res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	iterations := sink.iterationEvents()
	if len(iterations) != 1 {
		t.Fatalf("iteration events = %d, want 1", len(iterations))
	}
	if iterations[0].Context != nil {
		t.Errorf("Context = %+v, want nil when WithContextBreakdown is unset", iterations[0].Context)
	}
	if got := iterations[0].Usage; got == nil || got.PromptTokens != 10 {
		t.Errorf("usage should still be published: %+v", got)
	}
}

func TestRun_ToolCallThenTextCompletes(t *testing.T) {
	t.Parallel()

	// Turn 1: model calls echo. Turn 2: model emits a text-only answer
	// (no tool calls) and the runner naturally completes — there is no
	// complete_task signal in this codebase, only the absence of tool
	// calls plus a final assistant message.
	provider := &fakeProvider{
		turns: [][]llm.CompletionChunk{
			{chunkToolCall("c1", "echo", `{"x":"hi"}`), chunkDone()},
			{chunkText("all done"), chunkDone()},
		},
	}

	reg := newRegistry(stubTool{name: "echo", result: "echo-result"})

	r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(reg), runner.WithMaxIterations(5))
	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "ping",
	})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if res.Reason != runner.TerminalCompleted {
		t.Errorf("reason = %v, want completed", res.Reason)
	}
	if res.Iterations != 2 {
		t.Errorf("iterations = %d, want 2", res.Iterations)
	}
	if res.FinalContent != "all done" {
		t.Errorf("FinalContent = %q, want 'all done'", res.FinalContent)
	}
}

func TestRun_ToolCompletedEventCarriesEffects(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		turns: [][]llm.CompletionChunk{
			{chunkToolCall("c1", "edit", `{}`), chunkDone()},
			{chunkText("done"), chunkDone()},
		},
	}
	sink := newRecordingSink()
	effect := tools.NewFileEffect(tools.FileModify, "pkg/foo.go")
	reg := newRegistry(stubTool{name: "edit", result: "edited", effects: []tools.Effect{effect}})

	r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(reg), runner.WithMaxIterations(5), runner.WithSink(sink))
	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "edit the file",
	})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.toolCompletes) != 1 {
		t.Fatalf("tool completes = %d, want 1", len(sink.toolCompletes))
	}
	got := sink.toolCompletes[0].Effects
	if len(got) != 1 || got[0].File == nil || got[0].File.Path != "pkg/foo.go" || got[0].File.Op != tools.FileModify {
		t.Fatalf("effects = %+v, want one modify effect for pkg/foo.go", got)
	}
}

func TestRun_MaxIterationsCapsLoop(t *testing.T) {
	t.Parallel()

	// Provider always emits the same tool call — never settles on text.
	turns := make([][]llm.CompletionChunk, 5)
	for i := range turns {
		id := fmt.Sprintf("loop-%d", i)
		turns[i] = []llm.CompletionChunk{chunkToolCall(id, "echo", `{}`), chunkDone()}
	}
	provider := &fakeProvider{turns: turns}

	reg := newRegistry(stubTool{name: "echo"})

	r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(reg), runner.WithMaxIterations(3))
	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "loop",
	})
	if res.Reason != runner.TerminalMaxIterations {
		t.Errorf("reason = %v, want max_iterations", res.Reason)
	}
	if res.Iterations != 3 {
		t.Errorf("iterations = %d, want 3", res.Iterations)
	}
}

func TestRun_SinkEventsPublished(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		turns: [][]llm.CompletionChunk{
			{chunkText("answer"), chunkDone()},
		},
	}

	reg := newRegistry()

	sink := newRecordingSink()

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	t.Cleanup(cancel)

	r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(reg), runner.WithSink(sink), runner.WithMaxIterations(3))
	res := r.Run(ctx, runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "ping",
	})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}

	if got := sink.startedCount(); got != 1 {
		t.Errorf("ConversationStarted: got %d, want 1", got)
	}
	if got := sink.contentCount(); got == 0 {
		t.Error("Content never fired")
	}
	if got := sink.completedCount(); got != 1 {
		t.Errorf("completed events: got %d, want 1", got)
	}
}

// --- spawn_agent tests (now a registry tool, depth via ctx) ---

func TestRun_SpawnAgentRunsChildAndReturnsSummary(t *testing.T) {
	t.Parallel()

	// Three calls in sequence:
	//   parent turn 1 → spawn_agent
	//   child turn 1  → text-only answer (terminates child)
	//   parent turn 2 → text-only answer using the child's summary
	provider := &fakeProvider{
		turns: [][]llm.CompletionChunk{
			{chunkToolCall("p1", string(spawn.ToolNameSpawnAgent), `{"prompt":"compute X"}`), chunkDone()},
			{chunkText("X = 42"), chunkDone()},
			{chunkText("parent saw child say X = 42"), chunkDone()},
		},
	}

	reg := newRegistry(stubTool{name: "echo"})

	r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(reg), runner.WithMaxIterations(5))
	reg.Register(spawn.New(r))
	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "delegate",
	})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if res.Reason != runner.TerminalCompleted {
		t.Fatalf("reason = %v, want completed", res.Reason)
	}
	if !strings.Contains(res.FinalContent, "X = 42") {
		t.Errorf("FinalContent = %q, want it to contain 'X = 42'", res.FinalContent)
	}
	if res.Iterations != 2 {
		t.Errorf("parent iterations = %d, want 2", res.Iterations)
	}
	if got := provider.callCount(); got != 3 {
		t.Errorf("provider call count = %d, want 3 (parent×2 + child×1)", got)
	}
}

func TestRun_SpawnAgentRespectsDepthCap(t *testing.T) {
	t.Parallel()

	// Each level just spawns again. With spawn.New(r), the depth-3
	// child should refuse to spawn a depth-4. Each parent finishes with
	// a text-only message after its child returns.
	provider := &fakeProvider{
		turns: [][]llm.CompletionChunk{
			// Depth 0 → spawn (becomes depth 1)
			{chunkToolCall("a", string(spawn.ToolNameSpawnAgent), `{"prompt":"down"}`), chunkDone()},
			// Depth 1 → spawn (becomes depth 2)
			{chunkToolCall("b", string(spawn.ToolNameSpawnAgent), `{"prompt":"down"}`), chunkDone()},
			// Depth 2 → spawn (becomes depth 3)
			{chunkToolCall("c", string(spawn.ToolNameSpawnAgent), `{"prompt":"down"}`), chunkDone()},
			// Depth 3 — refused. The deepest agent gets a tool error
			// back and then settles on text.
			{chunkToolCall("d", string(spawn.ToolNameSpawnAgent), `{"prompt":"down"}`), chunkDone()},
			{chunkText("hit cap"), chunkDone()},
			// Depth 2 sees its child done, finishes itself.
			{chunkText("d2 done"), chunkDone()},
			// Depth 1 same.
			{chunkText("d1 done"), chunkDone()},
			// Depth 0 same.
			{chunkText("d0 done"), chunkDone()},
		},
	}

	reg := newRegistry(stubTool{name: "echo"})

	r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(reg), runner.WithMaxIterations(10))
	// Pass WithMaxDepth(3) explicitly — the package default is 1
	// (single-hop delegation) which is fine for production but
	// would short-circuit this test, which exists specifically to
	// exercise the cap behaviour at deeper levels.
	reg.Register(spawn.New(r, spawn.WithMaxDepth(3)))
	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "go deep",
	})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if res.Reason != runner.TerminalCompleted {
		t.Errorf("root reason = %v, want completed", res.Reason)
	}
	// All 8 scripted turns must have been consumed: if depth-cap
	// short-circuited too aggressively the provider would have unused
	// turns; if it failed, the provider would error on the would-be
	// 4th-level child.
	if got := provider.callCount(); got != 8 {
		t.Errorf("provider calls = %d, want 8 (3 spawns + 1 refused + 4 settles)", got)
	}
}

// recordingSink captures every event for assertion. Embeds NopSink so
// adding new event methods to EventSink later doesn't force this fake
// to update — the goal of these tests isn't exhaustive event coverage.
type recordingSink struct {
	runner.NopSink
	mu            sync.Mutex
	contents      []runner.Content
	toolStarts    []runner.ToolStarted
	toolCompletes []runner.ToolCompleted
	toolFails     []runner.ToolFailed
	convStarts    []runner.ConversationStarted
	convEnded     []runner.ConversationEnded
	iterCompletes []runner.IterationCompleted
	steerInjects  []runner.SteerInjected
	compactions   []runner.CompactionApplied
}

func newRecordingSink() *recordingSink { return &recordingSink{} }

func (s *recordingSink) OnContent(e runner.Content) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.contents = append(s.contents, e)
}
func (s *recordingSink) OnToolStarted(e runner.ToolStarted) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolStarts = append(s.toolStarts, e)
}
func (s *recordingSink) OnToolCompleted(e runner.ToolCompleted) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolCompletes = append(s.toolCompletes, e)
}
func (s *recordingSink) OnToolFailed(e runner.ToolFailed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolFails = append(s.toolFails, e)
}
func (s *recordingSink) OnConversationStarted(e runner.ConversationStarted) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.convStarts = append(s.convStarts, e)
}
func (s *recordingSink) OnConversationEnded(e runner.ConversationEnded) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.convEnded = append(s.convEnded, e)
}
func (s *recordingSink) OnIterationCompleted(e runner.IterationCompleted) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.iterCompletes = append(s.iterCompletes, e)
}
func (s *recordingSink) OnSteerInjected(e runner.SteerInjected) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steerInjects = append(s.steerInjects, e)
}
func (s *recordingSink) OnCompactionApplied(e runner.CompactionApplied) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.compactions = append(s.compactions, e)
}

func (s *recordingSink) contentCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.contents)
}
func (s *recordingSink) startedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.convStarts)
}
func (s *recordingSink) completedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.convEnded {
		if e.Reason == runner.TerminalCompleted {
			n++
		}
	}
	return n
}
func (s *recordingSink) iterationEvents() []runner.IterationCompleted {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]runner.IterationCompleted(nil), s.iterCompletes...)
}
func (s *recordingSink) steerCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.steerInjects)
}
func (s *recordingSink) firstSteer() (runner.SteerInjected, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.steerInjects) == 0 {
		return runner.SteerInjected{}, false
	}
	return s.steerInjects[0], true
}

func TestRun_ToolTimeoutAbandonsUncooperativeTool(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		turns: [][]llm.CompletionChunk{
			{chunkToolCall("slow-1", "block", `{}`), chunkDone()},
			{chunkText("recovered"), chunkDone()},
		},
	}

	reg := newRegistry(blockingTool{name: "block", started: make(chan struct{})})
	r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(reg),
		runner.WithMaxIterations(3),
		runner.WithToolTimeout(20*time.Millisecond),
	)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	start := time.Now()
	res := r.Run(ctx, runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "call the blocking tool",
	})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Run took %s; tool timeout did not abandon blocking Execute", elapsed)
	}
	if res.Reason != runner.TerminalCompleted {
		t.Fatalf("reason = %v, want completed", res.Reason)
	}
	if res.FinalContent != "recovered" {
		t.Fatalf("FinalContent = %q, want recovered", res.FinalContent)
	}

	var toolMsg string
	for _, m := range res.Messages {
		if m.Role == "tool" && m.ToolCallID == "slow-1" {
			toolMsg = m.Content
			break
		}
	}
	if !strings.Contains(toolMsg, "exceeded the per-tool time budget") {
		t.Fatalf("timeout tool message = %q, want per-tool timeout", toolMsg)
	}
}

func TestRun_ZeroTimeoutOptionsDisableStreamGuards(t *testing.T) {
	t.Parallel()

	provider := &delayedProvider{delay: 30 * time.Millisecond, reply: "slow but fine"}
	r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(newRegistry()),
		runner.WithMaxIterations(1),
		runner.WithIterationTimeout(0),
		runner.WithStreamIdleTimeout(0),
	)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	res := r.Run(ctx, runner.TaskSpec{ID: taskscope.ID(uuid.NewString()), Prompt: "go"})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if res.Reason != runner.TerminalCompleted {
		t.Fatalf("reason = %v, want completed", res.Reason)
	}
	if res.FinalContent != "slow but fine" {
		t.Fatalf("FinalContent = %q, want slow but fine", res.FinalContent)
	}
}

type delayedProvider struct {
	delay time.Duration
	reply string
}

func (p *delayedProvider) Name() string { return "delayed" }

func (p *delayedProvider) Complete(ctx context.Context, _ llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	return func(yield func(llm.CompletionChunk, error) bool) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(p.delay):
		}
		if !yield(chunkText(p.reply), nil) {
			return
		}
		if !yield(chunkDone(), nil) {
			return
		}
	}, nil
}

type blockingTool struct {
	name    tools.ToolName
	started chan struct{}
}

func (b blockingTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: b.name, Description: "blocks forever"}
}

func (b blockingTool) Execute(context.Context, tools.ToolCall) (*tools.ToolResult, error) {
	close(b.started)
	select {}
}
