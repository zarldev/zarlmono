package runner_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestStandardCompactionProtectsNewToolBatch(t *testing.T) {
	for _, tokens := range []int{2399, 2400, 3999} {
		body := strings.Repeat("important evidence ", 200)
		client := runnertest.NewClient([][]llm.CompletionChunk{
			{runnertest.ChunkToolCall("a", "echo", `{}`), runnertest.ChunkToolCall("b", "echo", `{}`), {UsageReported: true, Usage: llm.Usage{PromptTokens: tokens, TotalTokens: tokens}}},
			{runnertest.ChunkText("done")},
		})
		opts := coderunner.StandardOptions(coderunner.Tuning{ContextWindow: 4000})
		opts = append(opts, runner.WithTools(tools.NewRegistry(runnertest.Tool{Name: "echo", Result: body})),
			// NewTiered has a 128 KiB minimum budget; use a small explicit
			// budget so this fixture tests eligibility under token pressure.
			runner.WithCompactor(coderunner.StandardCompactor(&compact.Tiered{TargetBytes: 10000}, 4000, 512)))
		old := strings.Repeat("old evidence ", 800)
		result := runner.New(client, opts...).Run(t.Context(), runner.TaskSpec{Prompt: "continue", Context: []llm.Message{
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "old", Type: "function", Function: llm.ToolCallFunction{Name: "echo", Arguments: `{}`}}}},
			{Role: llm.RoleTool, ToolCallID: "old", Content: old},
			{Role: llm.RoleAssistant, Content: "old result consumed"},
		}})
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		for _, message := range result.Messages {
			if message.Role != llm.RoleTool {
				continue
			}
			if message.ToolCallID == "old" {
				if (len(message.Content) < len(old)) != (tokens >= 2400) {
					t.Errorf("usage %d: unexpected old-result reduction", tokens)
				}
			} else if !strings.Contains(message.Content, body) {
				t.Errorf("usage %d: new result %s was clipped", tokens, message.ToolCallID)
			}
		}
	}
}
