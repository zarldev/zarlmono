package runner_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestPromptCacheRouteIsIndependentOfTaskIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, key string
		depth     int
	}{
		{name: "conversation", key: "conversation-route"},
		{name: "task fallback"},
		{name: "delegated task isolation", key: "conversation-route", depth: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &fakeProvider{turns: [][]llm.CompletionChunk{
				{{ToolCalls: []llm.ToolCall{{ID: "1", Function: llm.ToolCallFunction{Name: "echo", Arguments: `{}`}}}}},
				{{Content: "done"}},
				{{ToolCalls: []llm.ToolCall{{ID: "2", Function: llm.ToolCallFunction{Name: "echo", Arguments: `{}`}}}}},
				{{Content: "done"}},
			}}
			r := runner.New(runner.ClientFromProvider(provider),
				runner.WithTools(newRegistry(stubTool{name: "echo", result: "ok"})),
				runner.WithPromptCacheKey(tc.key),
			)
			for i, id := range []taskscope.ID{"turn-1", "turn-2"} {
				result := r.Run(t.Context(), runner.TaskSpec{ID: id, Depth: tc.depth, Prompt: "go"})
				if result.Err != nil || result.ID != id {
					t.Fatalf("task identity changed: result=%+v", result)
				}
				want := string(id)
				if tc.depth == 0 && tc.key != "" {
					want = tc.key
				}
				for _, index := range []int{2 * i, 2*i + 1} {
					if got := provider.request(index).Options["prompt_cache_key"]; got != want {
						t.Errorf("request %d key = %v, want %s", index, got, want)
					}
				}
			}
		})
	}
}
