package tools

import (
	"context"
	"fmt"
	"strings"

	tools "github.com/zarldev/zarlmono/zkit/ai/tools"

	"github.com/zarldev/zarlmono/zarlai/repository"
	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zarlai/taskrunner"
)

// StartTaskTool creates and enqueues a background research task.
type StartTaskTool struct {
	tasks      *repository.TaskRepo
	runner     *taskrunner.Runner
	workspaces *repository.WorkspaceRepo
}

// NewStartTaskTool constructs a StartTaskTool.
func NewStartTaskTool(tasks *repository.TaskRepo, runner *taskrunner.Runner, workspaces *repository.WorkspaceRepo) *StartTaskTool {
	return &StartTaskTool{tasks: tasks, runner: runner, workspaces: workspaces}
}

func (t *StartTaskTool) Definition() tools.ToolSpec {
	workspaceNames := []string{"default"}
	if t.workspaces != nil {
		if rows, err := t.workspaces.List(context.Background()); err == nil && len(rows) > 0 {
			workspaceNames = workspaceNames[:0]
			for _, w := range rows {
				workspaceNames = append(workspaceNames, w.Name)
			}
		}
	}

	return tools.ToolSpec{
		Name:        "start_task",
		Description: "Kick off an autonomous background research task when the user asks you to \"look into\", \"research\", \"investigate\", \"dig into\", \"find out about\" something that takes more than a quick web lookup — comparing options, multi-source synthesis, deep dives, collecting many links. Also use when the user explicitly says \"work on this in the background\" or \"come back to me with\". Returns a task ID immediately; the task runs autonomously, calls tools, and notifies the user when it's done. Do NOT use for one-shot questions that a single web_search or wiki_search would answer.",
		Parameters: service.Parameters{
			{Name: "prompt", Type: service.ParamString, Description: "The research prompt or instructions for the task.", Required: true},
			{Name: "max_iterations", Type: service.ParamInteger, Description: "Maximum number of iterations before stopping. Defaults to 20.", Required: false},
			{Name: "profile", Type: service.ParamString, Description: "Execution profile name. One of: default, researcher, coder. Defaults to 'default'.", Required: false, Enum: []string{"default", "researcher", "coder"}},
			{Name: "workspace", Type: service.ParamString, Description: "Workspace name for coder tasks (ignored for non-coder profiles). Omit to use the 'default' workspace.", Required: false, Enum: workspaceNames},
		}.ToJSONSchema(),
	}
}

func (t *StartTaskTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	prompt := call.Arguments.String("prompt", "")
	if prompt == "" {
		return tools.Failure(call.ID, tools.Validation("start_task", "prompt is required")), nil
	}

	maxIter := 20
	if v := call.Arguments.Float("max_iterations", 0); v > 0 {
		maxIter = int(v)
	}

	profile := call.Arguments.String("profile", "")
	workspace := call.Arguments.String("workspace", "")
	personName := service.PersonNameFromCtx(ctx)
	sessionID := service.SessionIDFromCtx(ctx)

	task, err := t.tasks.Create(ctx, prompt, personName, sessionID, "", profile, workspace, maxIter)
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("start_task", err)), nil
	}

	t.runner.Enqueue(repository.TaskID(task.ID))

	msg := fmt.Sprintf("Task started (ID: %s, profile: %s", task.ID, task.ProfileName)
	if task.WorkspaceName != "" {
		msg += fmt.Sprintf(", workspace: %s", task.WorkspaceName)
	}
	msg += "). I'll notify you when it completes."

	return tools.Success(call.ID, msg), nil
}

// TaskStatusTool lists recent background tasks or returns the full report for a specific task.
type TaskStatusTool struct {
	tasks *repository.TaskRepo
}

// NewTaskStatusTool constructs a TaskStatusTool.
func NewTaskStatusTool(tasks *repository.TaskRepo) *TaskStatusTool {
	return &TaskStatusTool{tasks: tasks}
}

func (t *TaskStatusTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "task_status",
		Description: "Check progress or retrieve results of background research tasks. Call when the user asks \"what are you working on\", \"any updates\", \"how's that task going\", \"what did you find out about X\", or references a task by ID. Without task_id: lists the 10 most recent tasks with status + summary. With task_id: returns the full findings report for that task.",
		Parameters: service.Parameters{
			{Name: "task_id", Type: service.ParamString, Description: "Optional: specific task ID to get the full report for.", Required: false},
		}.ToJSONSchema(),
	}
}

func (t *TaskStatusTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	taskID := call.Arguments.String("task_id", "")

	// Specific task — return full details
	if taskID != "" {
		task, err := t.tasks.Get(ctx, repository.TaskID(taskID))
		if err != nil {
			return tools.Failure(call.ID, tools.Transient("task_status", err)), nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "Task: %s\nStatus: %s\nPrompt: %s\nIterations: %d/%d\nCreated: %s\n",
			task.ID, task.Status, task.Prompt, task.Iterations, task.MaxIterations, task.CreatedAt)
		if task.Summary != "" {
			fmt.Fprintf(&sb, "\nFindings:\n%s", task.Summary)
		}
		return tools.Success(call.ID, sb.String()), nil
	}

	// List recent tasks
	tasks, _, err := t.tasks.List(ctx, 10, 0)
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("task_status", err)), nil
	}

	if len(tasks) == 0 {
		return tools.Success(call.ID, "No tasks found."), nil
	}

	var sb strings.Builder
	sb.WriteString("Background tasks:\n")
	for _, task := range tasks {
		prompt := task.Prompt
		if len(prompt) > 60 {
			prompt = prompt[:60] + "..."
		}
		fmt.Fprintf(&sb, "\n[%s] %s — %q (created: %s)", task.Status, task.ID, prompt, task.CreatedAt)
		switch task.Status {
		case "running":
			fmt.Fprintf(&sb, " [%d/%d iterations]", task.Iterations, task.MaxIterations)
		case "completed":
			summary := task.Summary
			if len(summary) > 200 {
				summary = summary[:200] + "..."
			}
			if summary != "" {
				fmt.Fprintf(&sb, "\n  Summary: %s", summary)
			}
		}
	}

	return tools.Success(call.ID, sb.String()), nil
}

// ScheduleTaskTool creates a recurring scheduled task.
type ScheduleTaskTool struct {
	tasks     *repository.TaskRepo
	scheduler *taskrunner.Scheduler
}

// NewScheduleTaskTool constructs a ScheduleTaskTool.
func NewScheduleTaskTool(tasks *repository.TaskRepo, scheduler *taskrunner.Scheduler) *ScheduleTaskTool {
	return &ScheduleTaskTool{tasks: tasks, scheduler: scheduler}
}

func (t *ScheduleTaskTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "schedule_task",
		Description: "Set up a recurring autonomous task that runs on a cron schedule. Use when the user says \"every morning\", \"daily\", \"each week\", \"every Monday\", \"check X regularly\", or asks for a standing briefing/report. Natural-language schedules are accepted (\"every morning\", \"every hour\", \"every monday\", \"weekly\") and converted, or pass a raw 5-field cron expression. For one-off work, use start_task instead.",
		Parameters: service.Parameters{
			{Name: "prompt", Type: service.ParamString, Description: "The task prompt or instructions.", Required: true},
			{Name: "schedule", Type: service.ParamString, Description: "Cron expression or natural language schedule (e.g. 'every morning', 'every hour', 'every monday').", Required: true},
			{Name: "profile", Type: service.ParamString, Description: "Execution profile name. Defaults to 'default'.", Required: false, Enum: []string{"default", "researcher"}},
		}.ToJSONSchema(),
	}
}

func (t *ScheduleTaskTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	prompt := call.Arguments.String("prompt", "")
	if prompt == "" {
		return tools.Failure(call.ID, tools.Validation("schedule_task", "prompt is required")), nil
	}

	schedule := call.Arguments.String("schedule", "")
	if schedule == "" {
		return tools.Failure(call.ID, tools.Validation("schedule_task", "schedule is required")), nil
	}

	cron := naturalToCron(schedule)
	profile := call.Arguments.String("profile", "")

	personName := service.PersonNameFromCtx(ctx)
	sessionID := service.SessionIDFromCtx(ctx)

	task, err := t.tasks.Create(ctx, prompt, personName, sessionID, cron, profile, "", 20)
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("schedule_task", err)), nil
	}

	t.scheduler.Reload(ctx)

	return tools.Success(call.ID, fmt.Sprintf("Task scheduled (ID: %s, profile: %s) with schedule %q.", task.ID, task.ProfileName, cron)), nil
}

// naturalToCron converts common natural language phrases to cron expressions.
// Falls back to returning the input as-is when no phrase matches.
func naturalToCron(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "every morning":
		return "0 8 * * *"
	case "every evening":
		return "0 18 * * *"
	case "every hour":
		return "0 * * * *"
	case "daily", "every day":
		return "0 9 * * *"
	case "every monday":
		return "0 9 * * 1"
	case "every tuesday":
		return "0 9 * * 2"
	case "every wednesday":
		return "0 9 * * 3"
	case "every thursday":
		return "0 9 * * 4"
	case "every friday":
		return "0 9 * * 5"
	case "weekly":
		return "0 9 * * 1"
	default:
		return s
	}
}
