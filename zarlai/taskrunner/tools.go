package taskrunner

import (
	"context"
	"fmt"

	"github.com/zarldev/zarlmono/zarlai/service"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

// Runner tool names used for loop control detection.
const (
	ToolCompleteTask   = "complete_task"
	ToolReportProgress = "report_progress"
	ToolPauseTask      = "pause_task"
)

// RunnerTools returns the task lifecycle tools.
func RunnerTools() []tools.Tool {
	return []tools.Tool{
		&completeTaskTool{},
		&reportProgressTool{},
		&pauseTaskTool{},
	}
}

type completeTaskTool struct{}

func (t *completeTaskTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolCompleteTask,
		Description: "Signal that the task is complete. YOU MUST CALL THIS to finish a task — it concatenates with any report_progress findings to form the markdown report the user sees. The summary argument should be a self-contained final report in markdown (with headings, bullets, links, tables as appropriate). A task that returns without calling this shows an empty report to the user.",
		Parameters: service.Parameters{
			{Name: "summary", Type: service.ParamString, Description: "Final findings in full markdown (headings, bullets, links). This is what the user reads — make it complete, not a one-liner.", Required: true},
		}.ToJSONSchema(),
	}
}

func (t *completeTaskTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	summary := call.Arguments.String("summary", "")
	return tools.Success(call.ID, fmt.Sprintf("Task marked complete: %s", summary)), nil
}

type reportProgressTool struct{}

func (t *reportProgressTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolReportProgress,
		Description: "Record an intermediate finding so it appears in the final report. Each call appends its finding to the report markdown the user sees at the end. Call this after any web_search, read, or inspection step that produced a durable insight — otherwise the reasoning is lost when the task completes.",
		Parameters: service.Parameters{
			{Name: "finding", Type: service.ParamString, Description: "Markdown-formatted finding (title + 1-3 sentences + any links). One self-contained note per call.", Required: true},
		}.ToJSONSchema(),
	}
}

func (t *reportProgressTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	finding := call.Arguments.String("finding", "")
	return tools.Success(call.ID, fmt.Sprintf("Progress recorded: %s", finding)), nil
}

type pauseTaskTool struct{}

func (t *pauseTaskTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolPauseTask,
		Description: "Pause the task because progress is blocked. Use this when you cannot continue without additional information or resources.",
		Parameters: service.Parameters{
			{Name: "reason", Type: service.ParamString, Description: "Why the task cannot continue.", Required: true},
		}.ToJSONSchema(),
	}
}

func (t *pauseTaskTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	reason := call.Arguments.String("reason", "")
	return tools.Success(call.ID, fmt.Sprintf("Task paused: %s", reason)), nil
}
