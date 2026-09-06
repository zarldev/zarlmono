package runner_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestRunnerToolResultParts(t *testing.T) {
	t.Parallel()
	for _, success := range []bool{true, false} {
		name := "success"
		if !success {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			uri := "data:image/png;base64," + strings.Repeat("YWJj", 20*1024)
			tool := attachmentTool{result: &tools.ToolResult{
				Success: success, Data: strings.Repeat("metadata", 10*1024),
				Parts: []llm.ContentPart{llm.ImagePartFromDataURI(uri, "image/png")},
			}}
			client := &attachmentClient{Client: runnertest.NewClient([][]llm.CompletionChunk{
				{runnertest.ChunkToolCall("image-call", "image_tool", `{}`)},
				{runnertest.ChunkText("done")},
			})}
			r := runner.New(client, runner.WithTools(tools.NewRegistry(tool)))
			result := r.Run(t.Context(), runner.TaskSpec{Prompt: "look"})
			if result.Err != nil {
				t.Fatalf("Run: %v", result.Err)
			}
			if len(client.requests) != 2 {
				t.Fatalf("requests = %d, want 2", len(client.requests))
			}
			// Producer mutation must not change the runner-owned history.
			tool.result.Parts[0].Image.DataURI = "changed"
			raw, err := json.Marshal(result.Messages)
			if err != nil {
				t.Fatalf("marshal history: %v", err)
			}
			var restored []llm.Message
			if err := json.Unmarshal(raw, &restored); err != nil {
				t.Fatalf("restore history: %v", err)
			}
			for _, messages := range [][]llm.Message{client.requests[1], result.Messages, restored} {
				found := false
				for _, message := range messages {
					if message.Role != llm.RoleTool {
						continue
					}
					found = true
					if message.ToolCallID != "image-call" || strings.Contains(message.Content, "base64,") {
						t.Fatal("tool association lost or image flattened into text")
					}
					if !success {
						if len(message.Parts) != 0 {
							t.Fatal("failed tool attachments delivered")
						}
						continue
					}
					if !strings.Contains(message.Content, "truncat") {
						t.Fatal("oversized metadata should still be text-truncated")
					}
					if len(message.Parts) != 1 || message.Parts[0].Image == nil || message.Parts[0].Image.DataURI != uri {
						t.Fatal("image payload lost, aliased, or truncated")
					}
				}
				if !found {
					t.Fatal("missing tool result")
				}
			}
		})
	}
}

type attachmentTool struct {
	result *tools.ToolResult
}

func (attachmentTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: "image_tool", Description: "returns an image"}
}

func (tool attachmentTool) Execute(context.Context, tools.ToolCall) (*tools.ToolResult, error) {
	return tool.result, nil
}

type attachmentClient struct {
	*runnertest.Client
	requests [][]llm.Message
}

func (client *attachmentClient) Complete(ctx context.Context, req llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		client.requests = append(client.requests, llm.CloneMessages(req.Messages))
		client.Client.Complete(ctx, req)(yield)
	}
}
