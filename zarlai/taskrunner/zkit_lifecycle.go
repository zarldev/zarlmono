package taskrunner

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/zarldev/zarlmono/zarlai/repository"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
	znotify "github.com/zarldev/zarlmono/zkit/znotify"
)

// runState accumulates the lifecycle tools' output across one zkit Run so the
// post-Run finalisation (finaliseZkit) can build the report and choose the
// terminal status — information the zkit TaskResult can't carry, since the
// runner has no notion of complete_task/pause_task. One instance per
// executeTaskZkit call. Guarded because WithToolConcurrency(>1) could fire
// tools from multiple goroutines (zarlai uses 1, but stay safe).
type runState struct {
	mu        sync.Mutex
	findings  []string
	completed bool
	paused    bool
	summary   string
	reason    string
}

func (s *runState) addFinding(f string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.findings = append(s.findings, f)
}

func (s *runState) markComplete(summary string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.findings = append(s.findings, summary)
	s.summary = summary
	s.completed = true
}

func (s *runState) markPaused(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paused = true
	s.reason = reason
}

// runSnapshot is an immutable copy of runState for the post-Run path.
type runSnapshot struct {
	findings  []string
	completed bool
	paused    bool
	summary   string
	reason    string
}

func (s *runState) snapshot() runSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := make([]string, len(s.findings))
	copy(f, s.findings)
	return runSnapshot{findings: f, completed: s.completed, paused: s.paused, summary: s.summary, reason: s.reason}
}

// progressUpdater is the consumer-side view of *repository.TaskRepo.UpdateProgress
// so report_progress is testable without a live DB.
type progressUpdater interface {
	UpdateProgress(ctx context.Context, id repository.TaskID, iterations int, summary string) error
}

// The three stateful lifecycle tools embed their stateless stub for Definition()
// (identical LLM-facing spec) and override Execute. The legacy loop keeps using
// the stubs via RunnerTools(); runnerToolsFor mints these for the zkit path.

// zkitCompleteTool only records the final summary; the heavy persistence
// (qdrant/Complete/obsidian/pushReport) is centralised in finaliseZkit so it
// runs exactly once per task regardless of how many times the model calls
// complete_task or whether the loop ends via completion or max-iterations.
type zkitCompleteTool struct {
	completeTaskTool
	state *runState
}

func (t *zkitCompleteTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	summary := call.Arguments.String("summary", "")
	if summary == "" {
		return tools.Failure(call.ID, tools.Validation(ToolCompleteTask, "summary is required")), nil
	}
	t.state.markComplete(summary)
	return tools.Success(call.ID, fmt.Sprintf("Task marked complete: %s", summary)), nil
}

// zkitReportTool records a finding and fires the per-call effects the legacy
// handleReportProgress did inline (progress notification, UpdateProgress, bus
// emit via the emit closure). The [i/max] index is dropped — the sink's
// "calling report_progress" notification already carries it.
type zkitReportTool struct {
	reportProgressTool
	state    *runState
	notify   notifier
	progress progressUpdater
	emit     func(finding string) // bound to Runner.emitFinding(task, …)
	task     repository.Task
}

func (t *zkitReportTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	finding := call.Arguments.String("finding", "")
	if finding == "" {
		return tools.Failure(call.ID, tools.Validation(ToolReportProgress, "finding is required")), nil
	}
	t.state.addFinding(finding)
	if t.notify != nil {
		t.notify.Push(znotify.Notification{
			SessionID: t.task.SessionID,
			ToolName:  "task_runner",
			Content:   fmt.Sprintf("Task %s: %s", shortID(t.task.ID), truncate(finding, 300)),
			Broadcast: true,
		})
	}
	if t.progress != nil {
		// Iteration index isn't available to the tool; the running finding count
		// is a monotonic progress marker.
		count := len(t.state.snapshot().findings)
		if err := t.progress.UpdateProgress(ctx, repository.TaskID(t.task.ID), count, finding); err != nil {
			slog.ErrorContext(ctx, "zkit update task progress", "task_id", t.task.ID, "err", err)
		}
	}
	if t.emit != nil {
		t.emit(finding)
	}
	return tools.Success(call.ID, fmt.Sprintf("Progress recorded: %s", finding)), nil
}

// zkitPauseTool records the pause + pushes the pause notification; the
// "paused" status write is deferred to finaliseZkit (under the zkit loop a tool
// can't stop Run, so writing status here would be clobbered post-Run).
type zkitPauseTool struct {
	pauseTaskTool
	state  *runState
	notify notifier
	task   repository.Task
}

func (t *zkitPauseTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	reason := call.Arguments.String("reason", "")
	if reason == "" {
		return tools.Failure(call.ID, tools.Validation(ToolPauseTask, "reason is required")), nil
	}
	t.state.markPaused(reason)
	if t.notify != nil {
		t.notify.Push(znotify.Notification{
			SessionID: t.task.SessionID,
			ToolName:  "task_runner",
			Content:   fmt.Sprintf("Task %s paused: %s", shortID(t.task.ID), truncate(reason, 300)),
			Broadcast: true,
		})
	}
	return tools.Success(call.ID, fmt.Sprintf("Task paused: %s", reason)), nil
}

// runnerToolsFor mints the stateful lifecycle tools bound to a per-task runState
// + the Runner's deps. Replaces RunnerTools() on the zkit path.
func (r *Runner) runnerToolsFor(st *runState, task repository.Task) []tools.Tool {
	report := &zkitReportTool{
		state: st,
		task:  task,
		emit:  func(f string) { r.emitFinding(task, f) },
	}
	pause := &zkitPauseTool{state: st, task: task}
	if r.notifications != nil {
		report.notify = r.notifications
		pause.notify = r.notifications
	}
	if r.tasks != nil {
		report.progress = r.tasks
	}
	return []tools.Tool{
		&zkitCompleteTool{state: st},
		report,
		pause,
	}
}

// shortID renders a stable short id without the legacy task.ID[:8] panic risk.
func shortID(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}
