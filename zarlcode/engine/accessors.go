package engine

import (
	"context"
	"iter"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// RunTarget is a read-only snapshot of the re-pointable run target — the
// provider/model/window/limits a turn would be built against right now.
type RunTarget struct {
	Provider     llm.Provider
	Spec         ProviderSpec
	Model        string
	Window       int    // context window in tokens; sizes the compactor
	Reserve      int    // compactor reserve tokens; 0 = liveReserveTokens default
	MaxIter      int    // agent-loop cap per turn; 0 = built-in default
	SpawnMaxIter int    // sub-agent loop cap per spawn_agent; 0 = inherit MaxIter
	SpawnDepth   int    // sub-agent recursion ceiling; 0 = spawning disabled
	SearxngURL   string // web_search endpoint; empty leaves the tool unregistered
	Plan         bool   // PLAN mode: read-only tool surface + planning prompt
}

// RunTarget snapshots the current run target under the lock. It is the read
// counterpart to the SetProvider/SetLimits/SetContextWindow setters.
func (l *LiveRunner) RunTarget() RunTarget {
	if l == nil {
		return RunTarget{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.target
}

// ParentContext returns the application context bound via SetContext, or
// context.Background when none is set. Exposed for callers that need the
// run-lifetime context (e.g. re-pointing the provider mid-session).
func (l *LiveRunner) ParentContext() context.Context { return l.parentContext() }

// ProcessList returns a snapshot of background processes managed by the live
// runner, or nil when no process manager is wired. This is the lightweight
// counterpart to Inspect: it skips prompt/catalog rendering and only touches
// the process manager.
func (l *LiveRunner) ProcessList() []code.ProcessInfo {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	pm := l.pm
	l.mu.Unlock()
	if pm == nil {
		return nil
	}
	return pm.List()
}

// PopQueuedInput removes and returns the next queued steer message, reporting
// whether one was present.
func (l *LiveRunner) PopQueuedInput() (llm.Message, bool) { return l.popQueuedInput() }

// The Queue* methods expose the per-turn steer queue to the TUI's steer tray
// without surfacing the queue type itself. All are nil-safe.

// QueueSnapshot returns the queued steer messages.
func (l *LiveRunner) QueueSnapshot() []QueuedMessage {
	if l == nil || l.queue == nil {
		return nil
	}
	return l.queue.Snapshot()
}

// QueueLen reports how many steer messages are queued.
func (l *LiveRunner) QueueLen() int {
	if l == nil || l.queue == nil {
		return 0
	}
	return l.queue.Len()
}

// DrainQueue yields the queued steer messages for the current depth, draining
// them — the same operation the runner performs between iterations.
func (l *LiveRunner) DrainQueue(ctx context.Context) iter.Seq[llm.Message] {
	if l == nil || l.queue == nil {
		return func(func(llm.Message) bool) {}
	}
	return l.queue.Drain(ctx)
}

// QueueAppend appends a steer message, returning the new queue depth and the
// message id (for later QueueUpdate/QueueRemove).
func (l *LiveRunner) QueueAppend(text string) (int, int) {
	if l == nil || l.queue == nil {
		return 0, 0
	}
	return l.queue.Append(text)
}

// QueueClear drops all queued steer messages, returning how many were removed.
func (l *LiveRunner) QueueClear() int {
	if l == nil || l.queue == nil {
		return 0
	}
	return l.queue.Clear()
}

// QueueAppendControl appends a control-row steer message, returning the new
// length and the message id.
func (l *LiveRunner) QueueAppendControl(text string) (int, int) {
	if l == nil || l.queue == nil {
		return 0, 0
	}
	return l.queue.AppendControl(text)
}

// QueueRemove drops the queued message with the given id.
func (l *LiveRunner) QueueRemove(id int) bool {
	if l == nil || l.queue == nil {
		return false
	}
	return l.queue.Remove(id)
}

// QueueUpdate rewrites the text of the queued message with the given id.
func (l *LiveRunner) QueueUpdate(id int, text string) bool {
	if l == nil || l.queue == nil {
		return false
	}
	return l.queue.Update(id, text)
}

// QueueInjector returns an mcp.Injector-compatible adapter bound to the live
// steer queue, so MCP server notifications enqueue into the same queue as
// user-entered mid-run input.
func (l *LiveRunner) QueueInjector() QueueInjectorAdapter {
	return QueueInjectorAdapter{queue: l.queue}
}

// WorkspaceRoot returns the workspace root the settings were opened against.
func (s *Settings) WorkspaceRoot() string {
	if s == nil {
		return ""
	}
	return s.wsRoot
}
