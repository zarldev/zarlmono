package runner_test

import (
	"context"
	"iter"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// loopingProvider emits a tool call every turn until the runner
// hits its iteration cap. Used to drive finalize-warn tests where
// we need the loop to actually reach the threshold window rather
// than exit early on a text-only reply.
type loopingProvider struct {
	iter     atomic.Int32
	toolName string

	mu       sync.Mutex
	requests [][]llm.Message // messages sent on each Complete call
}

// requestsWithUserContaining counts how many captured requests carried a
// user message containing substr — i.e. how many times the model actually
// saw the nudge (it rides the request, not the canonical history).
func (p *loopingProvider) requestsWithUserContaining(substr string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, msgs := range p.requests {
		for _, m := range msgs {
			if m.Role == "user" && strings.Contains(m.Content, substr) {
				n++
				break
			}
		}
	}
	return n
}

func (p *loopingProvider) Complete(_ context.Context, req llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	p.mu.Lock()
	p.requests = append(p.requests, req.Messages)
	p.mu.Unlock()
	return func(yield func(llm.CompletionChunk, error) bool) {
		n := p.iter.Add(1)
		yield(llm.CompletionChunk{
			ToolCalls: []llm.ToolCall{{
				ID:   "tc-" + string('a'+(n%26)),
				Type: "function",
				Function: llm.ToolCallFunction{
					Name:      p.toolName,
					Arguments: `{"i":` + string('0'+(n%10)) + `}`,
				},
			}},
		}, nil)
	}, nil
}

func (p *loopingProvider) Name() string { return "looping" }

// trivialTool always succeeds so the loop keeps going turn after turn.
type trivialTool struct{ name string }

func (t *trivialTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        tools.ToolName(t.name),
		Description: "trivial",
		Parameters:  llm.Schema{Type: "object", AdditionalProperties: true},
	}
}
func (t *trivialTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	return &tools.ToolResult{ToolCallID: call.ID, Success: true, Data: "ok"}, nil
}

// --- unit tests for the default-message helper ---

func TestFinalizeWarn_DefaultMessageMentionsRemaining(t *testing.T) {
	t.Parallel()
	prov := &loopingProvider{toolName: "noop"}
	r := runner.New(
		runner.ClientFromProvider(prov),
		runner.WithTools(tools.NewRegistry()), // empty registry — tools will dispatch with "not found"
		runner.WithMaxIterations(1),
		runner.WithFinalizeWarn(runner.FinalizeWarn{RemainingThreshold: 1}),
	)
	// Just run it — even a single iteration with threshold=1 trips.
	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "go",
	})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	// The nudge rides the request, never the canonical history.
	var warning string
	prov.mu.Lock()
	for _, msgs := range prov.requests {
		for _, m := range msgs {
			if m.Role == "user" && strings.Contains(m.Content, "iteration") {
				warning = m.Content
			}
		}
	}
	prov.mu.Unlock()
	if warning == "" {
		t.Fatal("no request user message containing 'iteration' — default warning missing")
	}
	for _, want := range []string{"Wrap up", "plan or checklist", "signing off"} {
		if !strings.Contains(warning, want) {
			t.Errorf("warning = %q, want %q guidance", warning, want)
		}
	}
	if x := countIterationNudges(res.Messages); x != 0 {
		t.Errorf("nudge leaked into canonical history %d times, want 0", x)
	}
}

// --- end-to-end tests ---

func TestRunner_FinalizeWarnFiresAtThreshold(t *testing.T) {
	t.Parallel()
	reg := tools.NewRegistry()
	reg.Register(&trivialTool{name: "noop"})

	prov := &loopingProvider{toolName: "noop"}
	r := runner.New(
		runner.ClientFromProvider(prov),
		runner.WithTools(reg),
		runner.WithMaxIterations(6),
		runner.WithFinalizeWarn(runner.FinalizeWarn{RemainingThreshold: 3}),
	)
	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "go",
	})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if res.Reason != runner.TerminalMaxIterations {
		t.Fatalf("Reason = %q, want max-iterations (looping provider should exhaust the cap)", res.Reason)
	}

	// Fires once: exactly one request carries the nudge, and it never
	// enters the canonical history.
	if got := prov.requestsWithUserContaining("iteration"); got != 1 {
		t.Errorf("nudge appeared in %d requests, want exactly 1", got)
	}
	if got := countIterationNudges(res.Messages); got != 0 {
		t.Errorf("nudge leaked into canonical history %d times, want 0", got)
	}
}

func TestRunner_FinalizeWarnDoesNotFireWhenDisabled(t *testing.T) {
	t.Parallel()
	// Default zero value of FinalizeWarn has RemainingThreshold=0,
	// which disables the hook. A run that never installs the option
	// (the path most consumers take today) must see no warning.
	reg := tools.NewRegistry()
	reg.Register(&trivialTool{name: "noop"})

	r := runner.New(
		runner.ClientFromProvider(&loopingProvider{toolName: "noop"}),
		runner.WithTools(reg),
		runner.WithMaxIterations(4),
		// no WithFinalizeWarn
	)
	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "go",
	})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if warnings := countIterationNudges(res.Messages); warnings != 0 {
		t.Errorf("warning fired %d times without WithFinalizeWarn, want 0", warnings)
	}
}

func TestRunner_FinalizeWarnRespectsCustomMessage(t *testing.T) {
	t.Parallel()
	reg := tools.NewRegistry()
	reg.Register(&trivialTool{name: "noop"})

	prov := &loopingProvider{toolName: "noop"}
	r := runner.New(
		runner.ClientFromProvider(prov),
		runner.WithTools(reg),
		runner.WithMaxIterations(4),
		runner.WithFinalizeWarn(runner.FinalizeWarn{
			RemainingThreshold: 2,
			Message:            "PRODUCE THE ANSWER LINE NOW",
		}),
	)
	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "go",
	})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if hits := prov.requestsWithUserContaining("PRODUCE THE ANSWER LINE NOW"); hits != 1 {
		t.Errorf("custom-message warning reached the model in %d requests, want 1", hits)
	}
	// And the default phrasing must NOT appear — override is total.
	if x := prov.requestsWithUserContaining("Wrap up"); x != 0 {
		t.Errorf("default 'Wrap up' phrasing leaked through custom message: %d requests", x)
	}
}

func TestRunner_FinalizeWarnFiresAcrossMultipleQualifyingIterations(t *testing.T) {
	t.Parallel()
	// MaxIterations=5, threshold=4 → remaining at iter=0 is 5 (not
	// <= 4), at iter=1 is 4 (fires), at iter=2 is 3 (already warned,
	// no second injection). Exactly one warning expected.
	reg := tools.NewRegistry()
	reg.Register(&trivialTool{name: "noop"})

	prov := &loopingProvider{toolName: "noop"}
	r := runner.New(
		runner.ClientFromProvider(prov),
		runner.WithTools(reg),
		runner.WithMaxIterations(5),
		runner.WithFinalizeWarn(runner.FinalizeWarn{RemainingThreshold: 4}),
	)
	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "go",
	})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	// Exactly one request carries the nudge even though iters 1..4 all
	// qualify; history never accumulates it.
	if x := prov.requestsWithUserContaining("iteration"); x != 1 {
		t.Errorf("nudge appeared in %d requests across 4 qualifying iterations, want exactly 1", x)
	}
	if x := countIterationNudges(res.Messages); x != 0 {
		t.Errorf("nudge leaked into canonical history %d times, want 0", x)
	}
}

// countIterationNudges counts user messages mentioning "iteration" — the
// finalize-warn nudge's signature word — used to assert the nudge never leaks
// into canonical history.
func countIterationNudges(messages []llm.Message) int {
	n := 0
	for _, m := range messages {
		if m.Role == "user" && strings.Contains(m.Content, "iteration") {
			n++
		}
	}
	return n
}
