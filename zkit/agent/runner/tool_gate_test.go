package runner_test

import (
	"context"
	"iter"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// countingTool records how many times it executed, so a gate test can
// assert a blocked tool never ran.
type countingTool struct {
	name    tools.ToolName
	mutates bool
	calls   int
}

func (c *countingTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: c.name, Description: string(c.name), Mutates: c.mutates}
}
func (c *countingTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	c.calls++
	return &tools.ToolResult{ToolCallID: call.ID, Success: true, Data: "ran", ExecutedAt: time.Now()}, nil
}

// toolNameCapturingProvider records the tool names offered on each
// completion request, so a test can assert a gated tool is hidden.
type toolNameCapturingProvider struct {
	mu        sync.Mutex
	turns     [][]llm.CompletionChunk
	calls     int
	seen      [][]string
	afterCall func(int)
}

func (p *toolNameCapturingProvider) Complete(_ context.Context, req llm.CompletionRequest) llm.CompletionStream {
	p.mu.Lock()
	names := make([]string, 0, len(req.Tools))
	for _, t := range req.Tools {
		names = append(names, t.Function.Name)
	}
	p.seen = append(p.seen, names)
	call := p.calls
	chunks := p.turns[call]
	p.calls++
	if p.afterCall != nil {
		p.afterCall(call)
	}
	p.mu.Unlock()
	return func(yield func(llm.CompletionChunk, error) bool) {
		for _, c := range chunks {
			if !yield(c, nil) {
				return
			}
		}
	}
}
func (p *toolNameCapturingProvider) Name() string { return "capturing" }

func (p *toolNameCapturingProvider) offeredOnFirstCall() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.seen) == 0 {
		return nil
	}
	return p.seen[0]
}

// TestRun_ToolGateHidesAndRefuses covers the spawn-mode enforcement
// mechanism: with a gate planted on the Run ctx, a disallowed tool is
// hidden from the LLM tool list AND refused (not executed) if the model
// calls it from memory anyway, while allowed tools run normally.
func TestRun_ToolGateHidesAndRefuses(t *testing.T) {
	t.Parallel()

	provider := &toolNameCapturingProvider{turns: [][]llm.CompletionChunk{
		{chunkToolCall("c1", "mutate", `{}`)}, // model calls the blocked tool
		{chunkText("done")},                   // then completes after the refusal
	}}
	mutate := &countingTool{name: "mutate"}
	read := &countingTool{name: "read"}
	reg := newRegistry(mutate, read)

	gate := func(spec tools.ToolSpec) bool { return spec.Name != "mutate" }
	ctx := runner.WithToolGate(t.Context(), gate)

	r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(reg), runner.WithMaxIterations(5))
	res := r.Run(ctx, runner.TaskSpec{ID: taskscope.ID(uuid.NewString()), Prompt: "go"})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}

	if mutate.calls != 0 {
		t.Errorf("blocked tool executed %d times; want 0", mutate.calls)
	}
	if res.Reason != runner.TerminalCompleted {
		t.Errorf("reason = %v; want completed (model recovers after the refusal)", res.Reason)
	}
	offered := provider.offeredOnFirstCall()
	for _, n := range offered {
		if n == "mutate" {
			t.Errorf("gated tool was offered to the LLM: %v", offered)
		}
	}
	if !containsStr(offered, "read") {
		t.Errorf("allowed tool missing from offered set: %v", offered)
	}
}

func TestRun_ToolSurfaceEventsDescribeExactGatedSnapshot(t *testing.T) {
	t.Parallel()

	provider := &toolNameCapturingProvider{turns: [][]llm.CompletionChunk{
		{chunkToolCall("c1", "read", `{}`)},
		{chunkText("done")},
	}}
	reg := newRegistry(&countingTool{name: "mutate"}, &countingTool{name: "read"})
	sink := newRecordingSink()
	ctx := runner.WithToolGate(t.Context(), func(spec tools.ToolSpec) bool { return spec.Name != "mutate" })

	r := runner.New(
		runner.ClientFromProvider(provider),
		runner.WithTools(reg),
		runner.WithSink(sink),
		runner.WithMaxIterations(5),
	)
	if res := r.Run(ctx, runner.TaskSpec{ID: taskscope.ID(uuid.NewString()), Prompt: "go"}); res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}

	if got := len(sink.iterCompletes); got != 2 {
		t.Fatalf("surface events = %d, want 2", got)
	}
	first := sink.iterCompletes[0].ToolSurface
	second := sink.iterCompletes[1].ToolSurface
	if first.Count != 1 {
		t.Errorf("first Count = %d, want exact post-gate count 1", first.Count)
	}
	if first.JSONBytes <= 0 || first.Fingerprint == "" {
		t.Errorf("first surface lacks accounting: %+v", first)
	}
	if first.Changed {
		t.Error("first surface Changed = true, want false")
	}
	if second.Fingerprint != first.Fingerprint || second.Changed {
		t.Errorf("stable second surface = %+v, first = %+v", second, first)
	}
}

func TestRun_ToolSurfaceEventReportsControlledChange(t *testing.T) {
	t.Parallel()

	reg := newRegistry(&countingTool{name: "read"})
	provider := &toolNameCapturingProvider{turns: [][]llm.CompletionChunk{
		{chunkToolCall("c1", "read", `{}`)},
		{chunkText("done")},
	}}
	provider.afterCall = func(call int) {
		if call == 0 {
			reg.Register(&countingTool{name: "search"})
		}
	}
	sink := newRecordingSink()

	r := runner.New(
		runner.ClientFromProvider(provider),
		runner.WithTools(reg),
		runner.WithSink(sink),
		runner.WithMaxIterations(5),
	)
	if res := r.Run(t.Context(), runner.TaskSpec{ID: taskscope.ID(uuid.NewString()), Prompt: "go"}); res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}

	if got := len(sink.iterCompletes); got != 2 {
		t.Fatalf("surface events = %d, want 2", got)
	}
	first := sink.iterCompletes[0].ToolSurface
	second := sink.iterCompletes[1].ToolSurface
	if first.Count != 1 || second.Count != 2 {
		t.Fatalf("surface counts = %d then %d, want 1 then 2", first.Count, second.Count)
	}
	if !second.Changed || second.Fingerprint == first.Fingerprint {
		t.Errorf("changed surface not reported: first=%+v second=%+v", first, second)
	}
}

// TestRun_NoGateOffersAndRunsEverything is the control: without a gate the
// same tool executes normally and is offered.
func TestRun_NoGateOffersAndRunsEverything(t *testing.T) {
	t.Parallel()

	provider := &toolNameCapturingProvider{turns: [][]llm.CompletionChunk{
		{chunkToolCall("c1", "mutate", `{}`)},
		{chunkText("done")},
	}}
	mutate := &countingTool{name: "mutate"}
	reg := newRegistry(mutate)

	r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(reg), runner.WithMaxIterations(5))
	if res := r.Run(t.Context(), runner.TaskSpec{ID: taskscope.ID(uuid.NewString()), Prompt: "go"}); res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if mutate.calls != 1 {
		t.Errorf("ungated tool executed %d times; want 1", mutate.calls)
	}
	if !containsStr(provider.offeredOnFirstCall(), "mutate") {
		t.Error("ungated tool should be offered to the LLM")
	}
}

// opaqueSource wraps a tools.Source but exposes ONLY the Iterable +
// Executor surface — deliberately NOT the registry-style
// Tool(name) (Tool, bool) lookup. It stands in for the guarded /
// composite / sourcechain wrappers the runner is configured with in
// production, where the dispatch backstop's old type-assertion lookup
// would fail and fall through to a zero-value spec.
type opaqueSource struct{ inner tools.Source }

func (o opaqueSource) Tools(ctx context.Context) iter.Seq[tools.Tool] { return o.inner.Tools(ctx) }
func (o opaqueSource) Execute(ctx context.Context, c tools.ToolCall) (*tools.ToolResult, error) {
	return o.inner.Execute(ctx, c)
}

// TestRun_ToolGateFailsClosedOnWrapperSource is the regression for the
// dispatch-backstop fail-open gap: when the source is a wrapper that
// doesn't expose a direct Tool(name) lookup, the gate must still resolve
// the real (Mutates:true) spec from the tool snapshot and REFUSE a hidden
// mutating tool the model calls from memory — not admit it via a
// zero-value spec. Mirrors the capability-based explore policy
// (!spec.Mutates).
func TestRun_ToolGateFailsClosedOnWrapperSource(t *testing.T) {
	t.Parallel()

	provider := &toolNameCapturingProvider{turns: [][]llm.CompletionChunk{
		{chunkToolCall("c1", "mutate", `{}`)}, // model calls the hidden mutating tool
		{chunkText("done")},                   // then completes after the refusal
	}}
	mutate := &countingTool{name: "mutate", mutates: true}
	read := &countingTool{name: "read"}
	source := opaqueSource{inner: newRegistry(mutate, read)}

	// Capability gate: explore mode admits only non-mutating tools.
	gate := func(spec tools.ToolSpec) bool { return !spec.Mutates }
	ctx := runner.WithToolGate(t.Context(), gate)

	r := runner.New(runner.ClientFromProvider(provider), runner.WithTools(source), runner.WithMaxIterations(5))
	res := r.Run(ctx, runner.TaskSpec{ID: taskscope.ID(uuid.NewString()), Prompt: "go"})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if mutate.calls != 0 {
		t.Errorf("mutating tool executed %d times through a wrapper source; want 0 (gate must fail closed)", mutate.calls)
	}
	offered := provider.offeredOnFirstCall()
	for _, n := range offered {
		if n == "mutate" {
			t.Errorf("mutating tool was offered to the LLM under explore gate: %v", offered)
		}
	}
	if !containsStr(offered, "read") {
		t.Errorf("non-mutating tool missing from offered set: %v", offered)
	}
}

func containsStr(haystack []string, needle string) bool {
	return slices.Contains(haystack, needle)
}
