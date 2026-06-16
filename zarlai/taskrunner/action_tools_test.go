package taskrunner_test

import (
	"context"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/taskrunner"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestStoreMemoryTool_definition(t *testing.T) {
	tool := taskrunner.NewStoreMemoryTool(nil)
	def := tool.Definition()
	if def.Name != "store_memory" {
		t.Errorf("expected store_memory, got %s", def.Name)
	}
}

func TestStoreMemoryTool_requires_person_and_fact(t *testing.T) {
	tool := taskrunner.NewStoreMemoryTool(nil)
	result, err := tool.Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Fatal("expected failure for missing arguments")
	}
}

func TestSpawnTaskTool_definition(t *testing.T) {
	tool := taskrunner.NewSpawnTaskTool(nil, nil)
	def := tool.Definition()
	if def.Name != "spawn_task" {
		t.Errorf("expected spawn_task, got %s", def.Name)
	}
}

func TestAdjustScheduleTool_definition(t *testing.T) {
	tool := taskrunner.NewAdjustScheduleTool(nil)
	def := tool.Definition()
	if def.Name != "adjust_schedule" {
		t.Errorf("expected adjust_schedule, got %s", def.Name)
	}
}

func TestNotifyUserTool_definition(t *testing.T) {
	tool := taskrunner.NewNotifyUserTool(nil)
	def := tool.Definition()
	if def.Name != "notify_user" {
		t.Errorf("expected notify_user, got %s", def.Name)
	}
}

func TestProposeToolTool_definition(t *testing.T) {
	tool := taskrunner.NewProposeToolTool(nil)
	def := tool.Definition()
	if def.Name != "propose_tool" {
		t.Errorf("expected propose_tool, got %s", def.Name)
	}
}

func TestStoreMemoryTool_nil_store_returns_error(t *testing.T) {
	tool := taskrunner.NewStoreMemoryTool(nil)
	result, err := tool.Execute(context.Background(), tools.ToolCall{Arguments: tools.ToolParameters{
		"person_name": "Alice",
		"fact":        "likes coffee",
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Fatal("expected failure when store is nil")
	}
}

func TestSpawnTaskTool_missing_prompt_returns_error(t *testing.T) {
	tool := taskrunner.NewSpawnTaskTool(nil, nil)
	result, err := tool.Execute(context.Background(), tools.ToolCall{Arguments: tools.ToolParameters{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Fatal("expected failure for missing prompt")
	}
}

func TestAdjustScheduleTool_missing_schedule_returns_error(t *testing.T) {
	tool := taskrunner.NewAdjustScheduleTool(nil)
	result, err := tool.Execute(context.Background(), tools.ToolCall{Arguments: tools.ToolParameters{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Fatal("expected failure for missing schedule")
	}
}

func TestNotifyUserTool_missing_message_returns_error(t *testing.T) {
	tool := taskrunner.NewNotifyUserTool(nil)
	result, err := tool.Execute(context.Background(), tools.ToolCall{Arguments: tools.ToolParameters{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Fatal("expected failure for missing message")
	}
}

func TestProposeToolTool_missing_fields_returns_error(t *testing.T) {
	tool := taskrunner.NewProposeToolTool(nil)
	result, err := tool.Execute(context.Background(), tools.ToolCall{Arguments: tools.ToolParameters{
		"tool_name": "my_tool",
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Fatal("expected failure for missing fields")
	}
}
