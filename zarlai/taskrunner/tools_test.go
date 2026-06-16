package taskrunner_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zarlai/taskrunner"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestRunnerToolsCount(t *testing.T) {
	tools := taskrunner.RunnerTools()
	if len(tools) != 3 {
		t.Errorf("RunnerTools() returned %d tools, want 3", len(tools))
	}
}

func TestRunnerToolNames(t *testing.T) {
	tools := taskrunner.RunnerTools()
	names := make(map[string]bool, len(tools))
	for _, tool := range tools {
		names[tool.Definition().Name.String()] = true
	}

	for _, want := range []string{
		taskrunner.ToolCompleteTask,
		taskrunner.ToolReportProgress,
		taskrunner.ToolPauseTask,
	} {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}
}

func TestRunnerToolDefinitions(t *testing.T) {
	for _, tool := range taskrunner.RunnerTools() {
		def := tool.Definition()
		t.Run(def.Name.String(), func(t *testing.T) {
			if def.Description == "" {
				t.Error("description is empty")
			}
			props, _ := def.Parameters.Map()["properties"].(map[string]any)
			if len(props) == 0 {
				t.Error("no parameters defined")
			}
			required, _ := def.Parameters.Map()["required"].([]string)
			if len(required) == 0 {
				t.Error("no required parameters")
			}
		})
	}
}

func TestCompleteTaskExecute(t *testing.T) {
	runnerTools := taskrunner.RunnerTools()
	var tool tools.Tool
	for _, candidate := range runnerTools {
		if candidate.Definition().Name.String() == taskrunner.ToolCompleteTask {
			tool = candidate
			break
		}
	}
	if tool == nil {
		t.Fatal("complete_task tool not found")
	}

	result, err := tool.Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{"summary": "all done"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success || result.Data == "" {
		t.Error("content is empty")
	}
}

func TestReportProgressExecute(t *testing.T) {
	runnerTools := taskrunner.RunnerTools()
	var tool tools.Tool
	for _, candidate := range runnerTools {
		if candidate.Definition().Name.String() == taskrunner.ToolReportProgress {
			tool = candidate
			break
		}
	}
	if tool == nil {
		t.Fatal("report_progress tool not found")
	}

	result, err := tool.Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{"finding": "found something"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success || result.Data == "" {
		t.Error("content is empty")
	}
}

func TestPauseTaskExecute(t *testing.T) {
	runnerTools := taskrunner.RunnerTools()
	var tool tools.Tool
	for _, candidate := range runnerTools {
		if candidate.Definition().Name.String() == taskrunner.ToolPauseTask {
			tool = candidate
			break
		}
	}
	if tool == nil {
		t.Fatal("pause_task tool not found")
	}

	result, err := tool.Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{"reason": "blocked on external API"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success || result.Data == "" {
		t.Error("content is empty")
	}
}
