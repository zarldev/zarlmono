package taskrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	znotify "github.com/zarldev/zarlmono/zkit/znotify"

	"github.com/zarldev/zarlmono/zarlai/repository"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

// notifier pushes progress notifications. Consumer-side view of
// *znotify.NotificationStore so the sink is testable with a capturing fake.
type notifier interface {
	Push(n znotify.Notification)
}

// toolCallLogger persists a tool invocation. Consumer-side view of
// *repository.ToolCallRepo so the sink doesn't need a live DB under test.
type toolCallLogger interface {
	Log(ctx context.Context, tc repository.ToolCall) error
}

// taskEventSink translates the zkit runner's event stream into the side
// effects the legacy runAgentLoop performs inline: a "calling X"
// progress notification when
// a tool dispatches, and a tool_calls-table row once it returns. One
// sink instance per task Run — it captures the task's session id and the
// logging context so the runner's ctx-less event methods can still
// persist rows after the run's own ctx unwinds.
//
// Concurrency: WithToolConcurrency(>1) fires the tool callbacks from
// multiple goroutines, so the pending-args map and the iteration counter
// are guarded. The zarlai runner installs WithToolConcurrency(1), but
// the sink stays safe regardless.
type taskEventSink struct {
	runner.NopSink

	sessionID   string
	taskShortID string
	maxIter     int

	notifications notifier
	toolCalls     toolCallLogger
	// providerFor resolves a tool's provider for the log row. Reads the
	// runner's global registry (the per-task source doesn't carry
	// provider tags), matching executeProfileTool today.
	providerFor func(tools.ToolName) string
	// logCtx persists tool-call rows independently of the run ctx so a
	// completion logged as the run unwinds still lands. Detached from
	// cancellation via context.WithoutCancel at construction.
	logCtx context.Context

	// currentIter tracks the most recently completed iteration (1-based)
	// so the "calling X" notification can show [i/max] the way
	// executeProfileTool does. Tools dispatch before the iteration's
	// IterationCompleted fires, so the displayed index is currentIter+1.
	currentIter atomic.Int64

	mu      sync.Mutex
	pending map[string]map[string]any // ToolID -> arguments, awaiting completion
}

// newTaskEventSink builds the per-task sink. taskCtx is the task's run
// context; its cancellation is stripped for the logging path so
// terminal tool-call rows still persist.
func (r *Runner) newTaskEventSink(taskCtx context.Context, sessionID, taskID string, maxIter int) *taskEventSink {
	short := taskID
	if len(short) > 8 {
		short = short[:8]
	}
	s := &taskEventSink{
		sessionID:   sessionID,
		taskShortID: short,
		maxIter:     maxIter,
		logCtx:      context.WithoutCancel(taskCtx),
		pending:     make(map[string]map[string]any),
	}
	// Guard the interface assignments: stuffing a nil *NotificationStore
	// or *ToolCallRepo into the interface field yields a typed-nil that
	// the == nil guards in notify/logCall can't see, so a partial-deps
	// runner would panic instead of no-op'ing.
	if r.notifications != nil {
		s.notifications = r.notifications
	}
	if r.toolCallRepo != nil {
		s.toolCalls = r.toolCallRepo
	}
	if r.registry != nil {
		s.providerFor = r.registry.ProviderFor
	}
	return s
}

// OnToolStarted records the call's arguments for the completion log and
// pushes the "calling X" progress notification.
func (s *taskEventSink) OnToolStarted(ev runner.ToolStarted) {
	s.mu.Lock()
	s.pending[ev.ToolID] = ev.Parameters
	s.mu.Unlock()

	s.notify(fmt.Sprintf("Task %s [%d/%d]: calling %s",
		s.taskShortID, s.currentIter.Load()+1, s.maxIter, ev.ToolName))
}

// OnToolCompleted logs a successful call.
func (s *taskEventSink) OnToolCompleted(ev runner.ToolCompleted) {
	s.logCall(ev.ToolID, ev.ToolName, ev.FormattedResult, "", int(ev.Duration.Milliseconds()))
}

// OnToolFailed logs a failed call. The user-facing Error string is what
// the log row records — the typed Err stays internal, matching the
// errStr the inline path persisted.
func (s *taskEventSink) OnToolFailed(ev runner.ToolFailed) {
	s.logCall(ev.ToolID, ev.ToolName, "", ev.Error, int(ev.Duration.Milliseconds()))
}

// OnIterationCompleted advances the iteration counter so subsequent
// "calling X" notifications show the right [i/max].
func (s *taskEventSink) OnIterationCompleted(ev runner.IterationCompleted) {
	s.currentIter.Store(int64(ev.Iter) + 1)
}

// logCall pops the pending arguments for the call, marshals them, and
// writes the tool_calls row. Args absence (no matching OnToolStarted) is
// tolerated — the row is still worth recording.
func (s *taskEventSink) logCall(toolID, toolName, result, errStr string, durationMs int) {
	if s.toolCalls == nil {
		return
	}
	s.mu.Lock()
	args := s.pending[toolID]
	delete(s.pending, toolID)
	s.mu.Unlock()

	argsJSON, err := json.Marshal(args)
	if err != nil {
		argsJSON = []byte("{}")
	}

	row := repository.ToolCall{
		SessionID:  s.sessionID,
		ToolName:   toolName,
		Args:       string(argsJSON),
		Result:     result,
		Error:      errStr,
		DurationMs: durationMs,
	}
	if s.providerFor != nil {
		row.Provider = s.providerFor(tools.ToolName(toolName))
	}
	if err := s.toolCalls.Log(s.logCtx, row); err != nil {
		slog.ErrorContext(s.logCtx, "log tool call", "tool", toolName, "err", err)
	}
}

// notify pushes a broadcast task-runner progress notification, matching
// the cues the inline loop emits. No-op when no store is wired.
func (s *taskEventSink) notify(content string) {
	if s.notifications == nil {
		return
	}
	s.notifications.Push(znotify.Notification{
		SessionID: s.sessionID,
		ToolName:  "task_runner",
		Content:   content,
		Broadcast: true,
	})
}
