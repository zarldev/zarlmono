package taskrunner

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/zarldev/zarlmono/zarlai/repository"
	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/options"
	znotify "github.com/zarldev/zarlmono/zkit/znotify"
)

// pickProvider resolves the llm.Provider for a model, reusing the exact
// per-model selection pickChatClient already does (factory + cache), then
// narrowing the chosen ChatClient to its underlying provider. Returns false
// when the client doesn't expose one (e.g. a test fake) — the caller then
// falls back to the legacy loop.
func (r *Runner) pickProvider(model string) (llm.Provider, bool) {
	if pa, ok := r.pickChatClient(model).(service.ProviderAware); ok {
		return pa.Provider(), true
	}
	return nil, false
}

// executeTaskZkit runs a task through zkit/agent/runner.Run — the default
// execution path — instead of the hand-rolled runAgentLoop. It wires the
// three seams: newPromptSource (system prompt), newTaskEventSink
// (tool-call telemetry + notifications), and buildTaskSource (per-task tool
// set). The conversation lock threads straight through (zkit's runner takes
// the same *runner.ConversationLock).
//
// The lifecycle tools (zkit_lifecycle.go) record complete_task /
// report_progress / pause_task into runState, and finaliseZkit persists
// findings (qdrant + tasks.Complete + obsidian + report push) and always
// writes a terminal status — no terminal reason can strand a task in
// "running". The legacy loop remains as the fallback when a task's model
// resolves to no streaming provider, and via WithZkitLoop(false) as the
// escape hatch.
func (r *Runner) executeTaskZkit(ctx context.Context, task repository.Task, resolved ResolvedProfile, prov llm.Provider) {
	maxIter := resolved.MaxIterations
	if task.MaxIterations > 0 && task.MaxIterations < maxIter {
		maxIter = task.MaxIterations
	}

	// System prompt comes from the prompt source; the task prompt is the spec.
	_, taskPrompt := r.buildPrompts(ctx, task, resolved)

	// Run-scoped state the lifecycle tools record into; finaliseZkit reads it.
	st := &runState{}

	opts := []options.Option[runner.Runner]{
		runner.WithTools(buildTaskSource(resolved, r.runnerToolsFor(st, task), r.excludeTools)),
		runner.WithSink(r.newTaskEventSink(ctx, task.SessionID, string(task.ID), maxIter)),
		runner.WithPrompt(r.newPromptSource()),
		runner.WithMaxIterations(maxIter),
	}
	if r.convLock != nil {
		opts = append(opts, runner.WithConversationLock(r.convLock))
	}
	rn := runner.New(runner.ClientFromProvider(prov), opts...)

	toolCtx := context.WithValue(ctx, service.CtxPersonName, task.PersonName)
	toolCtx = context.WithValue(toolCtx, service.CtxSessionID, task.SessionID)

	res := rn.Run(toolCtx, runner.TaskSpec{
		ID:            taskscope.ID(task.ID),
		Prompt:        taskPrompt,
		PromptVars:    promptVarsFor(taskPromptInput{PersonName: task.PersonName, Prompt: task.Prompt}, resolved),
		MaxIterations: maxIter,
	})

	r.finaliseZkit(ctx, toolCtx, task, resolved, st, res)
}

// finalAction is the terminal disposition of a zkit-run task.
type finalAction int

const (
	finalComplete finalAction = iota // persist the findings report (or synthesise)
	finalFailed                      // unrecoverable error
	finalRequeue                     // cancelled — back to pending
	finalPaused                      // pause_task fired without completion
)

// decideFinal maps a TaskResult reason + the lifecycle flags onto the task's
// terminal disposition. Pure, so it's unit-testable without infra. Every path
// returns an action that writes a status, so no outcome can strand a task in
// "running".
func decideFinal(reason runner.TerminalReason, completed, paused bool) finalAction {
	switch reason {
	case runner.TerminalError:
		return finalFailed
	case runner.TerminalCancelled:
		return finalRequeue
	}
	if paused && !completed {
		return finalPaused
	}
	return finalComplete
}

// finaliseZkit runs once after rn.Run, reproducing the merged
// handleCompleteTask + forceComplete behaviour: it always writes a terminal
// status, and on completion persists the findings report exactly once
// (qdrant + tasks.Complete + obsidian + pushReport + notification).
func (r *Runner) finaliseZkit(ctx, toolCtx context.Context, task repository.Task, resolved ResolvedProfile, st *runState, res runner.TaskResult) {
	id := repository.TaskID(task.ID)
	snap := st.snapshot()
	switch decideFinal(res.Reason, snap.completed, snap.paused) {
	case finalFailed:
		r.setStatus(ctx, id, "failed")
	case finalRequeue:
		r.setStatus(ctx, id, "pending")
	case finalPaused:
		// the pause tool already pushed its notification; just persist status.
		r.setStatus(ctx, id, "paused")
	case finalComplete:
		r.completeZkit(ctx, toolCtx, task, resolved, snap, res)
	}
}

// completeZkit persists the findings report. Mirrors handleCompleteTask, with
// forceComplete's salvage (synthesise from the transcript when no findings were
// recorded). Runs exactly once per task.
func (r *Runner) completeZkit(ctx, toolCtx context.Context, task repository.Task, resolved ResolvedProfile, snap runSnapshot, res runner.TaskResult) {
	id := repository.TaskID(task.ID)
	fullReport := strings.Join(snap.findings, "\n\n")
	if strings.TrimSpace(fullReport) == "" {
		// No findings recorded — salvage from the transcript (forceComplete path).
		fullReport = r.synthesiseFromResult(ctx, task, resolved, res)
	}
	summary := snap.summary
	if summary == "" {
		summary = firstLineOr(fullReport, lastFinding(snap.findings))
	}

	r.storeFindingsInQdrant(ctx, task, []string{fullReport})
	if err := r.tasks.Complete(ctx, id, res.Iterations, fullReport); err != nil {
		slog.ErrorContext(ctx, "zkit task complete", "task_id", task.ID, "err", err)
	}
	obsidianPath := r.persistReport(toolCtx, resolved.Tools, task, fullReport)
	if snap.completed {
		// emitFinding only on an explicit complete_task, matching the legacy
		// handleCompleteTask (forceComplete didn't emit).
		r.emitFinding(task, summary)
	}
	if r.notifications != nil {
		content := fmt.Sprintf("Task complete: %s", truncate(summary, 300))
		if res.Reason == runner.TerminalMaxIterations {
			content = fmt.Sprintf("Task complete: reached max iterations (%d). Last finding: %s", res.Iterations, truncate(summary, 300))
		}
		// The "Task complete:" prefix is load-bearing (frontend completion matcher).
		r.notifications.Push(znotify.Notification{
			SessionID: task.SessionID,
			ToolName:  "task_runner",
			Content:   content,
			Broadcast: true,
		})
	}
	r.pushReport(task, fullReport, obsidianPath)
}

// synthesiseFromResult adapts synthesiseReportFromHistory to the zkit
// TaskResult: it rebuilds a service.Message transcript from res.Messages and
// makes one summarising chat call. Returns "" when no chat client resolves
// (same no-regression contract as the legacy path).
func (r *Runner) synthesiseFromResult(ctx context.Context, task repository.Task, resolved ResolvedProfile, res runner.TaskResult) string {
	chat := r.pickChatClient(resolved.Model)
	if chat == nil {
		return ""
	}
	history := make([]service.Message, 0, len(res.Messages))
	for _, m := range res.Messages {
		history = append(history, service.Message{Role: m.Role, Content: m.Content})
	}
	return r.synthesiseReportFromHistory(ctx, chat, task, history)
}

// setStatus writes a terminal status, logging on failure.
func (r *Runner) setStatus(ctx context.Context, id repository.TaskID, status string) {
	if err := r.tasks.SetStatus(ctx, id, status); err != nil {
		slog.ErrorContext(ctx, "zkit task set status", "task_id", string(id), "status", status, "err", err)
	}
}
