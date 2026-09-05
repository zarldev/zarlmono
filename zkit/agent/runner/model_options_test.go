package runner_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestWithModelOptionsMergesPromptCacheEveryIteration(t *testing.T) {
	provider := &fakeProvider{turns: [][]llm.CompletionChunk{
		{{ToolCalls: []llm.ToolCall{{ID: "1", Type: "function", Function: llm.ToolCallFunction{Name: "echo", Arguments: `{}`}}}}},
		{{Content: "done"}},
	}}
	reg := newRegistry(stubTool{name: "echo", result: "ok"})
	r := runner.New(runner.ClientFromProvider(provider),
		runner.WithTools(reg),
		runner.WithMaxIterations(2),
		runner.WithModelOptions(llm.ModelOptions{"text_verbosity": "high", "prompt_cache_key": "caller"}),
	)

	result := r.Run(t.Context(), runner.TaskSpec{ID: "task-cache", Prompt: "go"})
	if result.Err != nil {
		t.Fatalf("Run: %v", result.Err)
	}
	for i := range 2 {
		opts := provider.request(i).Options
		if opts["text_verbosity"] != "high" || opts["prompt_cache_key"] != "task-cache" {
			t.Errorf("request %d options = %#v", i, opts)
		}
	}
}

func TestWithModelOptionsOwnsNestedValuesAtConstructionAndPerRequest(t *testing.T) {
	provider := &fakeProvider{turns: [][]llm.CompletionChunk{
		{{ToolCalls: []llm.ToolCall{{ID: "1", Type: "function", Function: llm.ToolCallFunction{Name: "echo", Arguments: `{}`}}}}},
		{{Content: "done"}},
	}}
	reg := newRegistry(stubTool{name: "echo", result: "ok"})
	nested := map[string]any{"items": []any{"original"}}
	input := llm.ModelOptions{"nested": nested}
	r := runner.New(runner.ClientFromProvider(provider),
		runner.WithTools(reg),
		runner.WithMaxIterations(2),
		runner.WithModelOptions(input),
	)
	nested["items"].([]any)[0] = "caller-mutated"

	result := r.Run(t.Context(), runner.TaskSpec{ID: "task-owned", Prompt: "go"})
	if result.Err != nil {
		t.Fatalf("Run: %v", result.Err)
	}
	first := provider.request(0).Options
	firstNested := first["nested"].(map[string]any)
	if got := firstNested["items"].([]any)[0]; got != "original" {
		t.Fatalf("construction clone value = %v", got)
	}
	firstNested["items"].([]any)[0] = "provider-mutated"
	second := provider.request(1).Options
	if got := second["nested"].(map[string]any)["items"].([]any)[0]; got != "original" {
		t.Fatalf("per-request clone value = %v", got)
	}
}
