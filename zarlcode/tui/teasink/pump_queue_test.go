package teasink_test

import (
	"strconv"
	"testing"
	"testing/synctest"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

func TestPumpQueueWraparoundPreservesFIFO(t *testing.T) {
	send, messages := recordingSend()
	sink := teasink.New(send)
	t.Cleanup(sink.Close)
	for batch := range 3 {
		for i := range 2000 {
			sink.OnThinking(t.Context(), runner.Thinking{Delta: strconv.Itoa(batch*2000 + i)})
		}
		sink.Drain()
	}
	got := messages()
	if len(got) != 6000 {
		t.Fatalf("messages = %d", len(got))
	}
	for i, message := range got {
		thinking, ok := message.(teasink.ThinkingMsg)
		if !ok || thinking.Delta != strconv.Itoa(i) {
			t.Fatalf("FIFO changed at %d", i)
		}
	}
}

func TestPumpQueueCloseWakesCapacityWaiters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink, release, _ := blockedPreviewSink(t)
		produced := make(chan struct{})
		ctx := t.Context()
		go func() {
			defer close(produced)
			for range 5000 {
				sink.OnThinking(ctx, runner.Thinking{Delta: "queued"})
			}
		}()
		synctest.Wait()
		overflowed := sink.Overflows() > 0
		closed := make(chan struct{})
		go func() { sink.Close(); close(closed) }()
		synctest.Wait()
		unblocked := false
		select {
		case <-produced:
			unblocked = true
		default:
		}
		// Close cannot cancel an already-entered arbitrary send callback. Release it
		// and join both owned goroutines before asserting, including failure paths.
		release()
		<-produced
		<-closed
		if !overflowed || !unblocked {
			t.Fatal("Close did not wake a capacity-blocked producer")
		}
	})
}
