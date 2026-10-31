package teasink_test

import (
	"slices"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

// blockedPreviewSink owns a pump blocked inside its first delivery. Cleanup
// releases that callback before joining the pump, including assertion failures.
func blockedPreviewSink(t *testing.T) (*teasink.Sink, func(), func() []tea.Msg) {
	t.Helper()
	send, messages := recordingSend()
	entered, release := make(chan struct{}), make(chan struct{})
	var first, unblock sync.Once
	sink := teasink.New(func(msg tea.Msg) {
		first.Do(func() { close(entered); <-release })
		send(msg)
	})
	open := func() { unblock.Do(func() { close(release) }) }
	t.Cleanup(func() { open(); sink.Close() })
	sink.OnThinking(t.Context(), runner.Thinking{Delta: "gate"})
	<-entered
	return sink, open, messages
}

func TestQueuedPreviewsReplaceOnlyObsoleteSnapshots(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink, release, messages := blockedPreviewSink(t)
		for i := range 512 {
			for _, task := range []string{"one", "two"} {
				sink.OnToolOutput(t.Context(), toolOutputEvent(task, 0, "shared", strconv.Itoa(i)))
			}
			sink.Flush()
		}
		release()
		sink.Drain()
		got := toolOutputMessages(messages())
		if len(got) != 2 || got[0].Text != "511" || got[1].Text != "511" || got[0].TaskID == got[1].TaskID {
			t.Fatalf("queued previews = %#v; want latest per execution scope", got)
		}
		if sink.Overflows() != 0 {
			t.Fatal("obsolete previews filled the queue")
		}
	})
}

func TestQueuedPreviewLifecycleAndContentFences(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink, release, messages := blockedPreviewSink(t)
		emit := func(text string) {
			sink.OnToolOutput(t.Context(), toolOutputEvent("task", 0, "call", text))
			sink.Flush()
		}
		emit("old")
		emit("before content")
		sink.OnContent(t.Context(), runner.Content{Delta: "content fence"})
		sink.Flush()
		emit("before terminal")
		sink.OnToolCompleted(t.Context(), runner.ToolCompleted{TaskID: "task", ExecutionID: "call", ToolID: "tool", ToolName: "bash"})
		emit("late discarded")
		sink.OnToolStarted(t.Context(), runner.ToolStarted{TaskID: "task", ExecutionID: "call", ToolID: "tool", ToolName: "bash"})
		emit("new invocation")
		release()
		sink.Drain()
		var got []string
		for _, msg := range messages()[1:] {
			switch msg := msg.(type) {
			case teasink.ToolOutputMsg:
				got = append(got, msg.Text)
			case teasink.ContentMsg:
				got = append(got, msg.Delta)
			case teasink.ToolCompletedMsg:
				got = append(got, "completed")
			case teasink.ToolStartedMsg:
				got = append(got, "started")
			default:
				t.Fatalf("unexpected message %T", msg)
			}
		}
		want := []string{"before content", "content fence", "before terminal", "completed", "started", "new invocation"}
		if !slices.Equal(got, want) {
			t.Fatalf("order = %v, want %v", got, want)
		}
	})
}

func TestQueuedPreviewDrainFence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink, release, messages := blockedPreviewSink(t)
		sink.OnToolOutput(t.Context(), toolOutputEvent("task", 0, "call", "before drain"))
		sink.Flush()
		drained := make(chan struct{})
		go func() { sink.Drain(); close(drained) }()
		// The owned Drain goroutine is blocked behind the first delivery.
		synctest.Wait()
		sink.OnToolOutput(t.Context(), toolOutputEvent("task", 0, "call", "after drain"))
		sink.Flush()
		release()
		<-drained
		sink.Drain()
		got := toolOutputMessages(messages())
		if len(got) != 2 || got[0].Text != "before drain" || got[1].Text != "after drain" {
			t.Fatalf("drain boundary crossed: %#v", got)
		}
	})
}
