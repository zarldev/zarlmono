package teasink

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
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
		delivered = append(delivered, string(msg.(testMsg)))
	}

	s := New(send)
	defer s.Close()

	s.dispatch(testMsg("one")) // panics inside send
	s.dispatch(testMsg("two")) // must still be delivered
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

	s := New(func(tea.Msg) {})
	close(s.stop) // simulate Close() having run; deliver() must see teardown

	// Drive deliver directly so the test is deterministic (no reliance
	// on racing the pump goroutine against the closed stop channel).
	s.send.Store(func() *sendFunc {
		fn := sendFunc(func(tea.Msg) { panic("send on closed channel") })
		return &fn
	}())
	s.deliver(testMsg("x"))

	if logOut := logBuf.String(); strings.Contains(logOut, "recovered panic") {
		t.Fatalf("teardown-race panic should be swallowed, but was logged: %q", logOut)
	}
}

type testMsg string
