package runner_test

import (
	"context"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestRunnerNestedToolResultParts(t *testing.T) {
	t.Parallel()
	const uri = "data:image/png;base64,cG5n"
	tool := nestedAttachmentTool{attachmentTool{result: &tools.ToolResult{
		Success: true, Data: "screenshot metadata",
		Parts: []llm.ContentPart{llm.ImagePartFromDataURI(uri, "image/png")},
	}}}
	client := runnertest.NewClient([][]llm.CompletionChunk{
		{runnertest.ChunkToolCall("parent", "image_tool", `{}`)},
		{runnertest.ChunkText("done")},
	})
	sink := &runnertest.Sink{}
	outputs := &recordingToolOutputSink{}
	r := runner.New(client, runner.WithTools(tools.NewRegistry(tool)), runner.WithSink(sink), runner.WithToolOutputSink(outputs))
	result := r.Run(t.Context(), runner.TaskSpec{Prompt: "look"})
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	completed, ok := sink.FirstToolCompleted()
	if !ok || completed.ParentToolID != "parent" || completed.ToolID != "child" || completed.FormattedResult != "screenshot metadata" {
		t.Fatalf("missing nested completion: %#v", completed)
	}
	if len(completed.Parts) != 1 || completed.Parts[0].Image.DataURI != uri {
		t.Fatal("nested completion lost or aliased screenshot")
	}
	if len(outputs.records) != 2 || outputs.records[0].ParentToolCallID != "parent" || len(outputs.records[0].Parts) != 1 || outputs.records[0].Parts[0].Image.DataURI != uri {
		t.Fatalf("nested durable history lost attachment: %+v", outputs.records)
	}
}

type nestedAttachmentTool struct{ attachmentTool }

func (tool nestedAttachmentTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	observer := tools.NestedToolObserverFromContext(ctx)
	child := tools.NestedToolCall{ParentID: call.ID, ChildID: "child", Call: tools.ToolCall{ID: "child", ToolName: "computer_observe"}}
	observer.OnNestedToolStarted(ctx, child)
	observer.OnNestedToolFinished(ctx, tools.NestedToolResult{NestedToolCall: child, Result: tool.result})
	tool.result.Parts[0].Image.DataURI = "producer-mutated"
	return tools.Success(call.ID, "done"), nil
}
