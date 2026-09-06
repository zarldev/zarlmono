package runner_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type streamCallRecorder struct {
	mu     sync.Mutex
	labels []string
}

func (r *streamCallRecorder) record(label string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.labels = append(r.labels, label)
}

func (r *streamCallRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.labels...)
}

type streamRecordingTool struct {
	name     tools.ToolName
	recorder *streamCallRecorder
}

func (t streamRecordingTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{Name: t.name, Description: "records reconstructed stream calls"}
}

func (t streamRecordingTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	label, _ := call.Arguments["label"].(string)
	t.recorder.record(label)
	return &tools.ToolResult{ToolCallID: call.ID, Success: true}, nil
}

func positionedStreamCall(index int, arguments string) llm.CompletionChunk {
	return llm.CompletionChunk{ToolCalls: []llm.ToolCall{{
		ID:          "call",
		Type:        "function",
		OutputIndex: llm.OutputPosition(index),
		Function: llm.ToolCallFunction{
			Name:      "record",
			Arguments: arguments,
		},
	}}}
}

func TestRunnerReconstructsToolCallAcrossOutputPositionChanges(t *testing.T) {
	tests := []struct {
		name   string
		chunks []llm.CompletionChunk
		label  string
	}{
		{
			name: "positioned then omitted",
			chunks: []llm.CompletionChunk{
				positionedStreamCall(0, `{"label":`),
				runnertest.ChunkToolCall("call", "record", `"positioned-nil"}`),
			},
			label: "positioned-nil",
		},
		{
			name: "omitted then positioned then omitted",
			chunks: []llm.CompletionChunk{
				runnertest.ChunkToolCall("call", "record", `{"label":`),
				positionedStreamCall(0, `"nil-positioned`),
				runnertest.ChunkToolCall("call", "record", `-nil"}`),
			},
			label: "nil-positioned-nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := &streamCallRecorder{}
			registry := tools.NewRegistry(streamRecordingTool{name: "record", recorder: recorder})
			client := runnertest.NewClient([][]llm.CompletionChunk{
				tt.chunks,
				{runnertest.ChunkText("done")},
			})
			r := runner.New(client, runner.WithTools(registry), runner.WithMaxIterations(2))

			result := r.Run(t.Context(), runner.TaskSpec{ID: taskscope.ID(uuid.NewString()), Prompt: "go"})
			if result.Err != nil {
				t.Fatalf("Run: %v", result.Err)
			}
			if got := recorder.snapshot(); len(got) != 1 || got[0] != tt.label {
				t.Fatalf("tool executions = %#v, want [%q]", got, tt.label)
			}
		})
	}
}

func TestRunnerRejectsCrossChunkToolCallIdentityConflictsBeforeExecution(t *testing.T) {
	tests := []struct {
		name   string
		chunks []llm.CompletionChunk
	}{
		{
			name: "second complete call with omitted position",
			chunks: []llm.CompletionChunk{
				runnertest.ChunkToolCall("call", "record", `{"label":"first"}`),
				runnertest.ChunkToolCall("call", "record", `{"label":"second"}`),
			},
		},
		{
			name: "function name changes",
			chunks: []llm.CompletionChunk{
				runnertest.ChunkToolCall("call", "record", `{"label":`),
				runnertest.ChunkToolCall("call", "other", `"changed"}`),
			},
		},
		{
			name: "omitted position matches multiple occurrences",
			chunks: []llm.CompletionChunk{
				positionedStreamCall(0, `{"label":"first"}`),
				positionedStreamCall(1, `{"label":"second"}`),
				runnertest.ChunkToolCall("call", "record", " "),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := &streamCallRecorder{}
			registry := tools.NewRegistry(
				streamRecordingTool{name: "record", recorder: recorder},
				streamRecordingTool{name: "other", recorder: recorder},
			)
			client := runnertest.NewClient([][]llm.CompletionChunk{tt.chunks})
			r := runner.New(client, runner.WithTools(registry), runner.WithMaxIterations(1))

			result := r.Run(t.Context(), runner.TaskSpec{ID: taskscope.ID(uuid.NewString()), Prompt: "go"})
			if !errors.Is(result.Err, runner.ErrAmbiguousToolCalls) {
				t.Fatalf("Run error = %v, want ErrAmbiguousToolCalls", result.Err)
			}
			if got := recorder.snapshot(); len(got) != 0 {
				t.Fatalf("tool executions = %#v, want none", got)
			}
		})
	}
}
