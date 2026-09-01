package teasink_test

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
)

// TestSink_PumpSurvivesPanic asserts that a panic in the send function
// is logged (not silently swallowed) and that the pump keeps delivering
// subsequent messages rather than wedging.
func TestSink_PumpSurvivesPanic(t *testing.T) {
	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelError})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	var mu sync.Mutex
	var delivered []string
	first := true
	send := func(msg tea.Msg) {
		mu.Lock()
		defer mu.Unlock()
		if first {
			first = false
			panic("boom: malformed dispatch")
		}
		delivered = append(delivered, msg.(teasink.ThinkingMsg).Delta)
	}

	s := teasink.New(send)
	defer s.Close()

	s.OnThinking(runner.Thinking{TaskID: taskscope.ID("task"), Delta: "one"}) // panics inside send
	s.OnThinking(runner.Thinking{TaskID: taskscope.ID("task"), Delta: "two"}) // must still be delivered

	s.Drain()

	mu.Lock()
	got := append([]string(nil), delivered...)
	mu.Unlock()

	if len(got) != 1 || got[0] != "two" {
		t.Fatalf("pump did not survive panic; delivered = %v, want [two]", got)
	}
	if logOut := logBuf.String(); !strings.Contains(logOut, "recovered panic") || !strings.Contains(logOut, "boom") {
		t.Fatalf("expected the recovered panic to be logged, got: %q", logOut)
	}
}

// TestSink_TeardownPanicNotLogged asserts that a panic raised after the
// sink is shutting down (s.stop closed) is treated as the benign
// program-teardown race and swallowed without an error log.
func TestSink_TeardownPanicNotLogged(t *testing.T) {
	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelError})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	var s *teasink.Sink
	s = teasink.New(func(tea.Msg) {
		s.Close()
		panic("send on closed channel")
	})

	// Closing during delivery models the program-teardown race through the
	// public API: recovery observes shutdown and suppresses the panic log.
	s.OnThinking(runner.Thinking{TaskID: taskscope.ID("task"), Delta: "x"})
	s.Drain()

	if logOut := logBuf.String(); strings.Contains(logOut, "recovered panic") {
		t.Fatalf("teardown-race panic should be swallowed, but was logged: %q", logOut)
	}
}
