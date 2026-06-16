package spawn_test

import (
	"context"
	"iter"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type scriptedProvider struct {
	turns [][]llm.CompletionChunk
	calls int
}

func (p *scriptedProvider) Complete(_ context.Context, _ llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	chunks := p.turns[p.calls]
	p.calls++
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
func (p *scriptedProvider) Name() string { return "scripted" }

type countTool struct {
	name    tools.ToolName
	mutates bool
	calls   int
}

func (c *countTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: c.name, Description: string(c.name), Mutates: c.mutates}
}
func (c *countTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	c.calls++
	return &tools.ToolResult{ToolCallID: call.ID, Success: true, Data: "ran", ExecutedAt: time.Now()}, nil
}

func toolCallChunk(id, name string) llm.CompletionChunk {
	return llm.CompletionChunk{ToolCalls: []llm.ToolCall{{
		ID: id, Type: "function",
		Function: llm.ToolCallFunction{Name: name, Arguments: "{}"},
	}}}
}
func textChunk(s string) llm.CompletionChunk { return llm.CompletionChunk{Content: s} }
func doneChunk() llm.CompletionChunk         { return llm.CompletionChunk{Done: true} }

// TestExecute_ModePolicyGatesChild proves the spawn tool plants its mode
// tool-policy on the child Run: an explore-mode child cannot execute the
// edit tool even when its model calls it, while the same call runs in
// implement mode.
func TestExecute_ModePolicyGatesChild(t *testing.T) {
	// Policy filters by Mutates capability, not by hardcoded name.
	// This blocks ALL mutating tools in explore mode.
	policy := func(m spawn.SpawnMode, spec tools.ToolSpec) bool {
		// Block mutating tools in explore mode
		if m == spawn.SpawnModeExplore && spec.Mutates {
			return false
		}
		return true
	}

	run := func(mode string) int {
		provider := &scriptedProvider{turns: [][]llm.CompletionChunk{
			{toolCallChunk("e1", "edit"), doneChunk()}, // child calls edit
			{textChunk("done"), doneChunk()},           // then completes
		}}
		edit := &countTool{name: "edit", mutates: true}
		reg := tools.NewRegistry()
		reg.Register(edit)
		parent := runner.New(runner.ClientFromProvider(provider), runner.WithTools(reg), runner.WithMaxIterations(5))
		tool := spawn.New(parent, spawn.WithMaxDepth(1), spawn.WithModeToolPolicy(policy))

		res, err := tool.Execute(context.Background(), tools.ToolCall{
			ID:        "c1",
			Arguments: tools.ToolParameters{"prompt": "do it", "mode": mode},
		})
		if err != nil {
			t.Fatalf("Execute(mode=%s): %v", mode, err)
		}
		if res == nil {
			t.Fatalf("Execute(mode=%s) returned nil result", mode)
		}
		return edit.calls
	}

	if got := run("explore"); got != 0 {
		t.Errorf("explore-mode child executed edit %d times; want 0 (gated)", got)
	}
	if got := run("implement"); got != 1 {
		t.Errorf("implement-mode child executed edit %d times; want 1 (full surface)", got)
	}
}
