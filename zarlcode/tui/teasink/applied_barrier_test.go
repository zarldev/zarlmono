package teasink_test

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

type appliedMarker struct {
	session    string
	generation uint64
	turn       string
}

func TestAfterEventsIsApplicationVisible(t *testing.T) {
	t.Parallel()
	var messages []tea.Msg
	sink := teasink.New(func(msg tea.Msg) { messages = append(messages, msg) })
	t.Cleanup(sink.Close)
	marker := appliedMarker{session: "source", generation: 3, turn: "turn"}
	sink.OnContent(t.Context(), runner.Content{TaskID: "turn", Delta: "pending"})
	sink.AfterEvents(marker)
	sink.Drain()
	if len(messages) != 2 {
		t.Fatalf("got %d messages, want content and application marker", len(messages))
	}
	if content, ok := messages[0].(teasink.ContentMsg); !ok || content.Delta != "pending" {
		t.Fatal("marker overtook pending content")
	}
	if got, ok := messages[1].(appliedMarker); !ok || got != marker {
		t.Fatal("application marker was consumed or changed by pump")
	}
}

func TestAfterEventsCannotOvertakeBlockedTimerFlush(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		blocked := make(chan struct{})
		var messages []tea.Msg
		sink := teasink.New(func(msg tea.Msg) {
			<-blocked
			messages = append(messages, msg)
		})
		defer sink.Close()
		var publishers sync.WaitGroup
		publishers.Go(func() {
			// Exceed the documented pump capacity while delivery is blocked.
			for range 5000 {
				sink.OnThinking(t.Context(), runner.Thinking{TaskID: "turn", Delta: "reasoning"})
			}
		})
		synctest.Wait()
		sink.OnContent(t.Context(), runner.Content{TaskID: "turn", Delta: "last content"})
		time.Sleep(teasink.CoalesceWindow())
		synctest.Wait() // timer flush is now blocked on the full pump queue
		marker := appliedMarker{session: "source", generation: 4, turn: "turn"}
		publishers.Go(func() { sink.AfterEvents(marker) })
		close(blocked)
		publishers.Wait()
		sink.Drain()
		contentIndex, markerIndex := -1, -1
		for i, msg := range messages {
			switch msg.(type) {
			case teasink.ContentMsg:
				contentIndex = i
			case appliedMarker:
				markerIndex = i
			}
		}
		if contentIndex < 0 || markerIndex <= contentIndex {
			t.Fatalf("timer content index %d, marker index %d", contentIndex, markerIndex)
		}
	})
}
