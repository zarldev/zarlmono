package taskrunner

import (
	"context"
	"testing"
	"time"

	znotify "github.com/zarldev/zarlmono/zkit/znotify"

	"github.com/zarldev/zarlmono/zarlai/repository"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

type fakeNotifier struct{ pushed []znotify.Notification }

func (f *fakeNotifier) Push(n znotify.Notification) { f.pushed = append(f.pushed, n) }

type fakeToolLogger struct{ rows []repository.ToolCall }

func (f *fakeToolLogger) Log(_ context.Context, tc repository.ToolCall) error {
	f.rows = append(f.rows, tc)
	return nil
}

func newTestSink(notes *fakeNotifier, log *fakeToolLogger) *taskEventSink {
	return &taskEventSink{
		sessionID:     "sess-1",
		taskShortID:   "abcd1234",
		maxIter:       20,
		notifications: notes,
		toolCalls:     log,
		providerFor:   func(n tools.ToolName) string { return "prov-" + n.String() },
		logCtx:        context.Background(),
		pending:       make(map[string]map[string]any),
	}
}

func TestTaskEventSinkSuccessLogsAndNotifies(t *testing.T) {
	notes := &fakeNotifier{}
	log := &fakeToolLogger{}
	s := newTestSink(notes, log)

	s.OnToolStarted(runner.ToolStarted{
		ToolID:     "call-1",
		ToolName:   "web_search",
		Parameters: map[string]any{"query": "gpus"},
	})
	s.OnToolCompleted(runner.ToolCompleted{
		ToolID:          "call-1",
		ToolName:        "web_search",
		FormattedResult: "found 3 results",
		Duration:        150 * time.Millisecond,
	})

	if len(notes.pushed) != 1 {
		t.Fatalf("notifications = %d, want 1", len(notes.pushed))
	}
	if got, want := notes.pushed[0].Content, "Task abcd1234 [1/20]: calling web_search"; got != want {
		t.Errorf("notification content = %q, want %q", got, want)
	}

	if len(log.rows) != 1 {
		t.Fatalf("log rows = %d, want 1", len(log.rows))
	}
	row := log.rows[0]
	if row.SessionID != "sess-1" || row.ToolName != "web_search" {
		t.Errorf("row identity = %+v", row)
	}
	if row.Args != `{"query":"gpus"}` {
		t.Errorf("row args = %q, want %q", row.Args, `{"query":"gpus"}`)
	}
	if row.Result != "found 3 results" || row.Error != "" {
		t.Errorf("row result/error = %q / %q", row.Result, row.Error)
	}
	if row.Provider != "prov-web_search" {
		t.Errorf("row provider = %q, want %q", row.Provider, "prov-web_search")
	}
	if row.DurationMs != 150 {
		t.Errorf("row duration = %d, want 150", row.DurationMs)
	}
	// pending entry must be cleared after completion.
	if len(s.pending) != 0 {
		t.Errorf("pending not cleared: %v", s.pending)
	}
}

func TestTaskEventSinkFailureLogsError(t *testing.T) {
	notes := &fakeNotifier{}
	log := &fakeToolLogger{}
	s := newTestSink(notes, log)

	s.OnToolStarted(runner.ToolStarted{ToolID: "call-2", ToolName: "read", Parameters: map[string]any{"path": "x"}})
	s.OnToolFailed(runner.ToolFailed{
		ToolID:   "call-2",
		ToolName: "read",
		Error:    "no such file",
		Duration: 5 * time.Millisecond,
	})

	if len(log.rows) != 1 {
		t.Fatalf("log rows = %d, want 1", len(log.rows))
	}
	row := log.rows[0]
	if row.Error != "no such file" || row.Result != "" {
		t.Errorf("row error/result = %q / %q", row.Error, row.Result)
	}
}

// The iteration counter must advance so later "calling X" notifications
// show the right [i/max], the way executeProfileTool numbers them.
func TestTaskEventSinkIterationCounter(t *testing.T) {
	notes := &fakeNotifier{}
	s := newTestSink(notes, &fakeToolLogger{})

	s.OnToolStarted(runner.ToolStarted{ToolID: "a", ToolName: "t"}) // iter 0 -> [1/20]
	s.OnIterationCompleted(runner.IterationCompleted{Iter: 0})
	s.OnToolStarted(runner.ToolStarted{ToolID: "b", ToolName: "t"}) // iter 1 -> [2/20]
	s.OnIterationCompleted(runner.IterationCompleted{Iter: 1})
	s.OnToolStarted(runner.ToolStarted{ToolID: "c", ToolName: "t"}) // iter 2 -> [3/20]

	want := []string{
		"Task abcd1234 [1/20]: calling t",
		"Task abcd1234 [2/20]: calling t",
		"Task abcd1234 [3/20]: calling t",
	}
	if len(notes.pushed) != len(want) {
		t.Fatalf("notifications = %d, want %d", len(notes.pushed), len(want))
	}
	for i, w := range want {
		if got := notes.pushed[i].Content; got != w {
			t.Errorf("notification[%d] = %q, want %q", i, got, w)
		}
	}
}

// newTaskEventSink truncates the task id to 8 chars, wires the
// registry's ProviderFor, detaches the log ctx from cancellation, and
// stays a safe no-op when notifications / tool-call repo are absent.
func TestNewTaskEventSinkWiring(t *testing.T) {
	reg := tools.NewRegistry()
	r := NewRunner(Config{}, WithRegistry(reg))

	ctx, cancel := context.WithCancel(t.Context())
	s := r.newTaskEventSink(ctx, "sess-9", "0123456789abcdef", 7)

	if s.taskShortID != "01234567" {
		t.Errorf("taskShortID = %q, want %q", s.taskShortID, "01234567")
	}
	if s.maxIter != 7 || s.sessionID != "sess-9" {
		t.Errorf("maxIter/sessionID = %d / %q", s.maxIter, s.sessionID)
	}
	if s.providerFor == nil {
		t.Fatal("providerFor not wired from registry")
	}

	// logCtx must survive cancellation of the task ctx.
	cancel()
	if err := s.logCtx.Err(); err != nil {
		t.Errorf("logCtx cancelled with task ctx: %v", err)
	}

	// No notifications / tool-call repo wired: callbacks must no-op, not
	// panic on a typed-nil interface.
	s.OnToolStarted(runner.ToolStarted{ToolID: "x", ToolName: "t"})
	s.OnToolCompleted(runner.ToolCompleted{ToolID: "x", ToolName: "t"})
}

// A completion with no matching OnToolStarted still records a row (with
// empty args) rather than dropping the call.
func TestTaskEventSinkCompletionWithoutStart(t *testing.T) {
	log := &fakeToolLogger{}
	s := newTestSink(&fakeNotifier{}, log)

	s.OnToolCompleted(runner.ToolCompleted{ToolID: "orphan", ToolName: "t", FormattedResult: "ok"})

	if len(log.rows) != 1 {
		t.Fatalf("log rows = %d, want 1", len(log.rows))
	}
	if log.rows[0].Args != "null" {
		t.Errorf("args = %q, want %q (json of nil map)", log.rows[0].Args, "null")
	}
}
