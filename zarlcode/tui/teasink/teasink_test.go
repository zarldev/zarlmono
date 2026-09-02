package teasink_test

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"

	tea "charm.land/bubbletea/v2"

	. "github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// recordingSend returns a send function that appends every delivered
// message to a mutex-guarded slice, and a helper to snapshot the slice.
func recordingSend() (func(tea.Msg), func() []tea.Msg) {
	var mu sync.Mutex
	var msgs []tea.Msg
	send := func(msg tea.Msg) {
		mu.Lock()
		msgs = append(msgs, msg)
		mu.Unlock()
	}
	snapshot := func() []tea.Msg {
		mu.Lock()
		defer mu.Unlock()
		out := make([]tea.Msg, len(msgs))
		copy(out, msgs)
		return out
	}
	return send, snapshot
}

func TestSink_ContentCoalescing(t *testing.T) {
	t.Run("same key merges deltas into single ContentMsg", func(t *testing.T) {
		send, snap := recordingSend()
		s := New(send)
		defer s.Close()

		s.OnContent(runner.Content{TaskID: taskscope.ID("task1"), Depth: 0, Delta: "hello"})
		s.OnContent(runner.Content{TaskID: taskscope.ID("task1"), Depth: 0, Delta: " world"})
		s.Drain()

		msgs := snap()
		if len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %d: %v", len(msgs), msgs)
		}
		cm, ok := msgs[0].(ContentMsg)
		if !ok {
			t.Fatalf("expected ContentMsg, got %T", msgs[0])
		}
		if cm.Delta != "hello world" {
			t.Errorf("delta = %q, want %q", cm.Delta, "hello world")
		}
		if cm.TaskID != "task1" || cm.Depth != 0 {
			t.Errorf("key = (%q, %d), want (%q, %d)", cm.TaskID, cm.Depth, "task1", 0)
		}
	})

	t.Run("different keys produce separate ContentMsgs in order", func(t *testing.T) {
		send, snap := recordingSend()
		s := New(send)
		defer s.Close()

		s.OnContent(runner.Content{TaskID: taskscope.ID("A"), Depth: 0, Delta: "a"})
		s.OnContent(runner.Content{TaskID: taskscope.ID("B"), Depth: 0, Delta: "b"})
		s.OnContent(runner.Content{TaskID: taskscope.ID("A"), Depth: 1, Delta: "c"})
		s.Drain()

		msgs := snap()
		if len(msgs) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(msgs))
		}
		want := []struct {
			taskID string
			depth  int
			delta  string
		}{
			{"A", 0, "a"},
			{"B", 0, "b"},
			{"A", 1, "c"},
		}
		for i, w := range want {
			cm, ok := msgs[i].(ContentMsg)
			if !ok {
				t.Fatalf("msg[%d]: expected ContentMsg, got %T", i, msgs[i])
			}
			if cm.TaskID != w.taskID || cm.Depth != w.depth || cm.Delta != w.delta {
				t.Errorf("msg[%d] = (%q, %d, %q), want (%q, %d, %q)",
					i, cm.TaskID, cm.Depth, cm.Delta, w.taskID, w.depth, w.delta)
			}
		}
	})
}

func TestSink_ToolEventsFlushContent(t *testing.T) {
	t.Run("OnToolStarted flushes pending content first", func(t *testing.T) {
		send, snap := recordingSend()
		s := New(send)
		defer s.Close()

		s.OnContent(runner.Content{TaskID: taskscope.ID("task1"), Depth: 0, Delta: "pre-tool chunk"})
		s.OnToolStarted(runner.ToolStarted{
			TaskID:   taskscope.ID("task1"),
			Depth:    0,
			ToolID:   "t1",
			ToolName: "read",
		})
		s.Drain()

		msgs := snap()
		if len(msgs) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(msgs))
		}
		if _, ok := msgs[0].(ContentMsg); !ok {
			t.Errorf("msg[0]: expected ContentMsg before tool event, got %T", msgs[0])
		}
		if _, ok := msgs[1].(ToolStartedMsg); !ok {
			t.Errorf("msg[1]: expected ToolStartedMsg, got %T", msgs[1])
		}
	})

	t.Run("OnToolCompleted flushes pending content first", func(t *testing.T) {
		send, snap := recordingSend()
		s := New(send)
		defer s.Close()

		s.OnContent(runner.Content{TaskID: taskscope.ID("task1"), Depth: 0, Delta: "result chunk"})
		s.OnToolCompleted(runner.ToolCompleted{
			TaskID:   taskscope.ID("task1"),
			Depth:    0,
			ToolID:   "t1",
			ToolName: "read",
		})
		s.Drain()

		msgs := snap()
		if len(msgs) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(msgs))
		}
		if _, ok := msgs[0].(ContentMsg); !ok {
			t.Errorf("msg[0]: expected ContentMsg before tool event, got %T", msgs[0])
		}
		if _, ok := msgs[1].(ToolCompletedMsg); !ok {
			t.Errorf("msg[1]: expected ToolCompletedMsg, got %T", msgs[1])
		}
	})
}

func TestSink_Drain(t *testing.T) {
	t.Run("blocks until prior messages delivered", func(t *testing.T) {
		send, snap := recordingSend()
		s := New(send)
		defer s.Close()

		// Use different (TaskID, Depth) keys so they don't coalesce.
		s.OnContent(runner.Content{TaskID: taskscope.ID("task1"), Depth: 0, Delta: "a"})
		s.OnContent(runner.Content{TaskID: taskscope.ID("task1"), Depth: 1, Delta: "b"})
		s.OnToolStarted(runner.ToolStarted{
			TaskID:   taskscope.ID("task1"),
			Depth:    0,
			ToolID:   "t1",
			ToolName: "read",
		})
		s.Drain()

		msgs := snap()
		if len(msgs) != 3 {
			t.Errorf("expected 3 messages after Drain, got %d", len(msgs))
		}
	})

	t.Run("returns immediately when pump not started", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := New(nil)
			done := make(chan struct{})
			go func() {
				s.Drain()
				close(done)
			}()

			synctest.Wait()
			select {
			case <-done:
				// OK — did not block.
			default:
				t.Fatal("Drain blocked despite pump not started")
			}
		})
	})
}

func TestSink_NilSendSafety(t *testing.T) {
	t.Run("events before SetSend do not panic", func(t *testing.T) {
		s := New(nil)

		// These must not panic.
		s.OnContent(runner.Content{TaskID: taskscope.ID("task1"), Depth: 0, Delta: "hello"})
		s.OnToolStarted(runner.ToolStarted{TaskID: taskscope.ID("task1"), Depth: 0, ToolID: "t1", ToolName: "read"})
		s.OnToolCompleted(runner.ToolCompleted{TaskID: taskscope.ID("task1"), Depth: 0, ToolID: "t1", ToolName: "read"})
		s.OnToolFailed(runner.ToolFailed{TaskID: taskscope.ID("task1"), Depth: 0, ToolID: "t1", ToolName: "read", Error: "boom"})
		s.OnConversationStarted(runner.ConversationStarted{TaskID: taskscope.ID("task1"), Depth: 0})
		s.OnConversationEnded(runner.ConversationEnded{TaskID: taskscope.ID("task1"), Depth: 0, Reason: runner.TerminalCompleted})
		s.OnConversationEnded(runner.ConversationEnded{TaskID: taskscope.ID("task1"), Depth: 0, Reason: runner.TerminalError, Error: "boom"})
		s.OnIterationCompleted(runner.IterationCompleted{TaskID: taskscope.ID("task1"), Depth: 0})
		s.OnSteerInjected(runner.SteerInjected{TaskID: taskscope.ID("task1"), Depth: 0})
		s.OnCompactionApplied(runner.CompactionApplied{TaskID: taskscope.ID("task1"), Depth: 0})
		s.Flush()
		s.Close()
	})

	t.Run("no messages delivered when send is nil", func(t *testing.T) {
		s := New(nil)

		s.OnContent(runner.Content{TaskID: taskscope.ID("task1"), Depth: 0, Delta: "hello"})
		s.OnToolStarted(runner.ToolStarted{TaskID: taskscope.ID("task1"), Depth: 0, ToolID: "t1", ToolName: "read"})
		// Flush dispatches through the pump, but since started is false
		// (no SetSend called), dispatch returns immediately.
		s.Flush()

		// Reaching here without panic is the assertion — no send function
		// means nothing to record.
	})
}

func TestSink_Close(t *testing.T) {
	t.Run("idempotent", func(t *testing.T) {
		s := New(nil)
		s.Close()
		s.Close() // must not panic
	})

	t.Run("post-close dispatch is no-op", func(t *testing.T) {
		send, snap := recordingSend()
		s := New(send)
		s.Close()

		// After Close, dispatch should silently drop.
		s.OnContent(runner.Content{TaskID: taskscope.ID("task1"), Depth: 0, Delta: "hello"})
		s.OnToolStarted(runner.ToolStarted{TaskID: taskscope.ID("task1"), Depth: 0, ToolID: "t1", ToolName: "read"})

		// Drain returns immediately because stop is closed.
		s.Drain()

		msgs := snap()
		if len(msgs) != 0 {
			t.Errorf("expected 0 messages after Close, got %d", len(msgs))
		}
	})
}

func TestSink_CloseWaitsForPump(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	entered := make(chan struct{})
	s := New(func(tea.Msg) {
		close(entered)
		<-release
	})
	s.OnToolStarted(runner.ToolStarted{TaskID: taskscope.ID("task"), ToolID: "tool", ToolName: "read"})
	<-entered
	closed := make(chan struct{})
	go func() {
		s.Close()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("Close returned before pump exited")
	default:
	}
	close(release)
	<-closed
	s.Close()
}

func TestSink_Overflows(t *testing.T) {
	t.Run("increments when pump buffer is full", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			sendBlocked := make(chan struct{})
			releaseSend := make(chan struct{})
			s := New(func(tea.Msg) {
				close(sendBlocked)
				select {
				case <-releaseSend:
				case <-t.Context().Done():
				}
			})
			defer s.Close()

			s.OnThinking(runner.Thinking{TaskID: taskscope.ID("blocked"), Delta: "x"})
			<-sendBlocked

			done := make(chan struct{})
			go func() {
				defer close(done)
				for range 4097 {
					s.OnThinking(runner.Thinking{TaskID: taskscope.ID("fill"), Delta: "x"})
				}
			}()

			synctest.Wait()
			if got := s.Overflows(); got != 1 {
				t.Fatalf("Overflows() = %d, want 1", got)
			}

			close(releaseSend)
			synctest.Wait()
			select {
			case <-done:
			default:
				t.Fatal("dispatch did not resume after the send function unblocked")
			}
		})
	})

	t.Run("starts at zero", func(t *testing.T) {
		s := New(nil)
		if got := s.Overflows(); got != 0 {
			t.Errorf("Overflows() = %d, want 0", got)
		}
	})
}

func TestSink_Flush(t *testing.T) {
	t.Run("idempotent", func(t *testing.T) {
		s := New(nil)
		s.Flush()
		s.Flush() // must not panic
	})

	t.Run("empty flush is no-op", func(t *testing.T) {
		send, snap := recordingSend()
		s := New(send)
		defer s.Close()

		s.Flush() // nothing pending
		s.Drain()

		msgs := snap()
		if len(msgs) != 0 {
			t.Errorf("expected 0 messages after empty flush, got %d", len(msgs))
		}
	})

	t.Run("flush dispatches pending then clears", func(t *testing.T) {
		send, snap := recordingSend()
		s := New(send)
		defer s.Close()

		s.OnContent(runner.Content{TaskID: taskscope.ID("task1"), Depth: 0, Delta: "hello"})
		s.Flush()
		s.Drain()

		msgs := snap()
		if len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %d", len(msgs))
		}
		cm := msgs[0].(ContentMsg)
		if cm.Delta != "hello" {
			t.Errorf("delta = %q, want %q", cm.Delta, "hello")
		}

		// Second flush after drain should produce nothing.
		s.Flush()
		s.Drain()
		msgs2 := snap()
		if len(msgs2) != 1 {
			t.Errorf("expected still 1 message after second flush, got %d", len(msgs2))
		}
	})
}

func TestSink_EmptyDelta(t *testing.T) {
	t.Run("OnContent with empty delta is no-op", func(t *testing.T) {
		send, snap := recordingSend()
		s := New(send)
		defer s.Close()

		s.OnContent(runner.Content{TaskID: taskscope.ID("task1"), Depth: 0, Delta: ""})
		s.Drain()

		msgs := snap()
		if len(msgs) != 0 {
			t.Errorf("expected 0 messages for empty delta, got %d", len(msgs))
		}
	})
}

func TestSink_SetSend(t *testing.T) {
	t.Run("nil SetSend clears the send function", func(t *testing.T) {
		send, snap := recordingSend()
		s := New(send)
		defer s.Close()

		// Deliver one event to confirm send is wired.
		s.OnContent(runner.Content{TaskID: taskscope.ID("task1"), Depth: 0, Delta: "before"})
		s.Drain()
		if len(snap()) != 1 {
			t.Fatal("expected 1 message before SetSend(nil)")
		}

		// Clear the send function.
		s.SetSend(nil)

		// This event should be silently dropped.
		s.OnContent(runner.Content{TaskID: taskscope.ID("task1"), Depth: 0, Delta: "after"})
		s.Drain()

		msgs := snap()
		if len(msgs) != 1 {
			t.Errorf("expected still 1 message after nil send, got %d", len(msgs))
		}
	})
}

func TestSink_ConcurrentSafety(t *testing.T) {
	t.Run("concurrent OnContent calls do not race", func(t *testing.T) {
		send, snap := recordingSend()
		s := New(send)
		defer s.Close()

		const goroutines = 10
		const callsPerG = 100
		var wg sync.WaitGroup
		wg.Add(goroutines)
		for g := range goroutines {
			go func(_ int) {
				defer wg.Done()
				for range callsPerG {
					s.OnContent(runner.Content{
						TaskID: taskscope.ID("task"),
						Depth:  0,
						Delta:  "x",
					})
				}
			}(g)
		}
		wg.Wait()
		s.Drain()

		msgs := snap()
		// Every chunk should be accounted for — coalescing may reduce
		// the message count, but the total delta length must match.
		totalLen := 0
		for _, m := range msgs {
			cm, ok := m.(ContentMsg)
			if !ok {
				t.Fatalf("unexpected message type: %T", m)
			}
			totalLen += len(cm.Delta)
		}
		if totalLen != goroutines*callsPerG {
			t.Errorf("total delta length = %d, want %d", totalLen, goroutines*callsPerG)
		}
	})
}

func TestSink_Diff(t *testing.T) {
	t.Run("Diff flushes content and dispatches DiffMsg", func(t *testing.T) {
		send, snap := recordingSend()
		s := New(send)
		defer s.Close()

		s.OnContent(runner.Content{TaskID: taskscope.ID("task1"), Depth: 0, Delta: "before diff"})
		s.Diff("file.go", "--- a/file.go\n+++ b/file.go\n@@ -1 +1 @@\n-old\n+new\n")
		s.Drain()

		msgs := snap()
		if len(msgs) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(msgs))
		}
		if _, ok := msgs[0].(ContentMsg); !ok {
			t.Errorf("msg[0]: expected ContentMsg, got %T", msgs[0])
		}
		dm, ok := msgs[1].(DiffMsg)
		if !ok {
			t.Fatalf("msg[1]: expected DiffMsg, got %T", msgs[1])
		}
		if dm.Path != "file.go" {
			t.Errorf("DiffMsg.Path = %q, want %q", dm.Path, "file.go")
		}
	})
}

func TestSink_PlanUpdated(t *testing.T) {
	t.Run("PlanUpdated flushes content and dispatches PlanUpdatedMsg", func(t *testing.T) {
		send, snap := recordingSend()
		s := New(send)
		defer s.Close()

		s.OnContent(runner.Content{TaskID: taskscope.ID("task1"), Depth: 0, Delta: "pre-plan"})
		s.PlanUpdated("task1", code.Plan{
			Steps:       []code.PlanStep{{Text: "step1", Status: code.StepStatuses.PENDING}},
			Explanation: "test plan",
		})
		s.Drain()

		msgs := snap()
		if len(msgs) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(msgs))
		}
		if _, ok := msgs[0].(ContentMsg); !ok {
			t.Errorf("msg[0]: expected ContentMsg, got %T", msgs[0])
		}
		if _, ok := msgs[1].(PlanUpdatedMsg); !ok {
			t.Errorf("msg[1]: expected PlanUpdatedMsg, got %T", msgs[1])
		}
	})
}

func TestSink_QueuedEventsOwnMutableValues(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	entered := make(chan struct{})
	var once sync.Once
	send, snap := recordingSend()
	s := New(func(msg tea.Msg) {
		once.Do(func() {
			close(entered)
			<-release
		})
		send(msg)
	})
	defer s.Close()

	// Occupy the pump so every event below remains queued while producer-owned
	// values are mutated after their callbacks return.
	s.OnThinking(runner.Thinking{TaskID: taskscope.ID("block"), Delta: "block"})
	<-entered

	args := tools.ToolParameters{"nested": map[string]any{"items": []any{"original"}}}
	completedEffects := []tools.Effect{tools.NewFileEffect(tools.FileModify, "original.go")}
	failedEffects := []tools.Effect{tools.NewProcessEffect("original", 0)}
	rateLimit := &llm.RateLimitError{Message: "original"}
	messages := []llm.Message{{
		Content:   "original",
		ToolCalls: []llm.ToolCall{{ID: "original"}},
		Parts:     []llm.ContentPart{llm.ImagePartFromURL("original")},
	}}

	s.OnToolStarted(runner.ToolStarted{Parameters: args})
	s.OnToolCompleted(runner.ToolCompleted{Effects: completedEffects})
	s.OnToolFailed(runner.ToolFailed{Effects: failedEffects})
	s.OnConversationEnded(runner.ConversationEnded{RateLimit: rateLimit})
	s.OnSteerInjected(runner.SteerInjected{Messages: messages})

	args["nested"].(map[string]any)["items"].([]any)[0] = "mutated"
	completedEffects[0].File.Path = "mutated.go"
	failedEffects[0].Process.Command = "mutated"
	rateLimit.Message = "mutated"
	messages[0].Content = "mutated"
	messages[0].ToolCalls[0].ID = "mutated"
	messages[0].Parts[0].Image.URL = "mutated"

	close(release)
	s.Drain()
	msgs := snap()
	if got := msgs[1].(ToolStartedMsg).Parameters["nested"].(map[string]any)["items"].([]any)[0]; got != "original" {
		t.Errorf("ToolStarted parameters = %q, want original", got)
	}
	if got := msgs[2].(ToolCompletedMsg).Effects[0].File.Path; got != "original.go" {
		t.Errorf("ToolCompleted effect path = %q, want original.go", got)
	}
	if got := msgs[3].(ToolFailedMsg).Effects[0].Process.Command; got != "original" {
		t.Errorf("ToolFailed effect command = %q, want original", got)
	}
	if got := msgs[4].(ConversationEndedMsg).RateLimit.Message; got != "original" {
		t.Errorf("rate-limit message = %q, want original", got)
	}
	steer := msgs[5].(SteerInjectedMsg).Messages[0]
	if steer.Content != "original" || steer.ToolCalls[0].ID != "original" || steer.Parts[0].Image.URL != "original" {
		t.Errorf("SteerInjected message retained producer mutation: %#v", steer)
	}
}

func TestSink_TimerCoalesceWindow(t *testing.T) {
	// Verify that the timer-based coalesce window dispatches content without an
	// explicit Flush or Drain. synctest advances time only after every goroutine
	// in this bubble is quiescent, so these assertions cannot race the timer or
	// pump.
	t.Run("timer fires and dispatches coalesced content", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			send, snap := recordingSend()
			s := New(send)
			defer s.Close()

			s.OnContent(runner.Content{TaskID: taskscope.ID("task1"), Depth: 0, Delta: "timer"})
			s.OnContent(runner.Content{TaskID: taskscope.ID("task1"), Depth: 0, Delta: " test"})

			// Advance the fake clock so the coalescing timer expires.
			time.Sleep(CoalesceWindow())
			synctest.Wait()

			msgs := snap()
			if len(msgs) != 1 {
				t.Fatalf("expected 1 message after timer fire, got %d", len(msgs))
			}
			cm := msgs[0].(ContentMsg)
			if cm.Delta != "timer test" {
				t.Errorf("delta = %q, want %q", cm.Delta, "timer test")
			}
		})
	})

	t.Run("second burst arms a fresh timer", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			send, snap := recordingSend()
			s := New(send)
			defer s.Close()

			// First burst.
			s.OnContent(runner.Content{TaskID: taskscope.ID("task1"), Depth: 0, Delta: "burst1"})
			// Advance the fake clock so the first burst is dispatched.
			time.Sleep(CoalesceWindow())
			synctest.Wait()

			// Second burst.
			s.OnContent(runner.Content{TaskID: taskscope.ID("task1"), Depth: 0, Delta: "burst2"})
			// Advance the fake clock so the re-armed timer dispatches the second burst.
			time.Sleep(CoalesceWindow())
			synctest.Wait()

			msgs := snap()
			if len(msgs) != 2 {
				t.Fatalf("expected 2 messages (one per burst), got %d", len(msgs))
			}
			if msgs[0].(ContentMsg).Delta != "burst1" {
				t.Errorf("first delta = %q, want %q", msgs[0].(ContentMsg).Delta, "burst1")
			}
			if msgs[1].(ContentMsg).Delta != "burst2" {
				t.Errorf("second delta = %q, want %q", msgs[1].(ContentMsg).Delta, "burst2")
			}
		})
	})
}

func TestSink_New(t *testing.T) {
	t.Run("New with nil send accepts a drain", func(t *testing.T) {
		s := New(nil)
		defer s.Close()
		s.Drain()
	})

	t.Run("SetSend starts delivery", func(t *testing.T) {
		s := New(nil)
		defer s.Close()
		s.SetSend(func(tea.Msg) {})
		s.OnThinking(runner.Thinking{TaskID: taskscope.ID("task"), Delta: "started"})
		s.Drain()
	})
}
