package tui_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

type launchBurstProvider struct {
	burst         chan struct{}
	waitForCancel bool
}

func (launchBurstProvider) Name() string { return "openai" }

func (p launchBurstProvider) Complete(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		for range 5000 {
			if !yield(llm.CompletionChunk{Thinking: "synthetic reasoning"}, nil) {
				return
			}
		}
		close(p.burst)
		if p.waitForCancel {
			<-ctx.Done()
			yield(llm.CompletionChunk{}, ctx.Err())
			return
		}
		yield(llm.CompletionChunk{Content: "done"}, nil)
	}
}

func TestLaunchHeadlessEventComposition(t *testing.T) {
	for _, control := range []bool{false, true} {
		for _, cancelled := range []bool{false, true} {
			name := map[bool]string{false: "launch headless", true: "legacy control"}[control] + "/" + map[bool]string{false: "burst completes", true: "burst then cancel"}[cancelled]
			t.Run(name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					ws, err := code.NewWorkspace(t.TempDir())
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = ws.Close() })
					sink := (tui.Launch{Headless: true}).NewLiveEventSink()
					if control {
						sink.Close()
						sink = teasink.New(nil)
					}
					t.Cleanup(sink.Close)
					provider := launchBurstProvider{burst: make(chan struct{}), waitForCancel: cancelled}
					live := engine.NewLiveRunner(provider, ws, "synthetic-model", engine.WithLiveSink(sink))
					t.Cleanup(func() {
						if err := live.Close(context.WithoutCancel(t.Context())); err != nil {
							t.Error(err)
						}
					})
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					var result runner.TaskResult
					var wg sync.WaitGroup
					wg.Go(func() { result = live.RunHeadless(ctx, "synthetic prompt", 1) })
					synctest.Wait()
					select {
					case <-provider.burst:
					default:
						t.Errorf("headless composition blocked before burst completed; overflows=%d", sink.Overflows())
						// Final abort releases a blocked publisher even on the old
						// composition, so the negative control never leaks a worker.
						sink.Close()
					}
					if cancelled {
						cancel()
					}
					wg.Wait()
					if cancelled {
						if !errors.Is(result.Err, context.Canceled) {
							t.Fatalf("headless cancellation = %s: %v", result.Reason, result.Err)
						}
					} else if result.Reason != runner.TerminalCompleted || result.Err != nil {
						t.Fatalf("headless result = %s: %v", result.Reason, result.Err)
					}
				})
			})
		}
	}
}

func TestLaunchInteractiveRetainsUnwiredApplicationReceipts(t *testing.T) {
	sink := (tui.Launch{}).NewLiveEventSink()
	t.Cleanup(sink.Close)
	sink.OnThinking(t.Context(), runner.Thinking{Delta: "retained before Run wires the consumer"})
	if err := sink.TakeApplications(t.Context()); err != nil {
		t.Fatal(err)
	}
	delivery, available, _ := sink.NextApplication()
	if !available || !delivery.Apply(func(msg tea.Msg) {
		event, ok := msg.(teasink.ThinkingMsg)
		if !ok || event.Delta != "retained before Run wires the consumer" {
			t.Errorf("interactive receipt = %#v", msg)
		}
	}) {
		t.Fatal("interactive composition discarded its unwired receipt")
	}
}
