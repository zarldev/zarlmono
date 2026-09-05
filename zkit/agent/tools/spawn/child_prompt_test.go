package spawn_test

import (
	"context"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type promptCaptureClient struct {
	request llm.CompletionRequest
}

func (c *promptCaptureClient) Complete(_ context.Context, request llm.CompletionRequest) llm.CompletionStream {
	c.request = request
	return func(yield func(llm.CompletionChunk, error) bool) {
		yield(llm.CompletionChunk{Content: "done"}, nil)
	}
}

func TestExecuteChildPromptRequiresAutonomousScopedCompletion(t *testing.T) {
	t.Parallel()

	client := &promptCaptureClient{}
	parent := runner.New(client, runner.WithMaxIterations(1), runner.WithSink(runner.NopSink{}))
	tool := spawn.New(parent, spawn.WithMaxDepth(1))
	result, err := tool.Execute(t.Context(), tools.ToolCall{
		ID:        "contract",
		Arguments: tools.ToolParameters{"prompt": "inspect the task"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute result: %+v", result)
	}
	if len(client.request.Messages) == 0 {
		t.Fatal("child received no messages")
	}
	prompt := strings.ToLower(client.request.Messages[len(client.request.Messages)-1].Content)
	for _, required := range []string{
		"work autonomously",
		"stay within scope",
		"destructive actions",
		"material scope expansion",
		"complete the requested work",
		"relevant verification",
		"perform it or state the blocker",
		"useful partial summary",
	} {
		if !strings.Contains(prompt, required) {
			t.Errorf("child prompt missing %q", required)
		}
	}
}
