package teasink_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

func TestApplicationDeliveredUnappliedTakeover(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sent := make(chan teasink.ApplicationDelivery, 1)
		sink := teasink.NewApplicationSink(func(msg tea.Msg) { sent <- msg.(teasink.ApplicationDelivery) })
		t.Cleanup(sink.Close)
		sink.AfterEvents("first")
		old := <-sent
		sink.AfterEvents("second")
		synctest.Wait()
		select {
		case <-sent:
			t.Fatal("more than one unacknowledged delivery")
		default:
		}
		if err := sink.TakeApplications(t.Context()); err != nil {
			t.Fatal(err)
		}
		if old.Apply(func(tea.Msg) { t.Fatal("stale callback") }) {
			t.Fatal("stale token applied")
		}
		for _, want := range []string{"first", "second"} {
			next, ok, _ := sink.NextApplication()
			if !ok {
				t.Fatal("missing retained payload")
			}
			if !next.Apply(func(msg tea.Msg) {
				if msg != want {
					t.Fatalf("payload %v, want %s", msg, want)
				}
			}) {
				t.Fatal("claim rejected")
			}
			if next.Apply(func(tea.Msg) { t.Fatal("duplicate callback") }) {
				t.Fatal("duplicate applied")
			}
		}
		_, ok, changed := sink.NextApplication()
		if ok {
			t.Fatal("unexpected entry")
		}
		sink.AfterEvents("wake")
		select {
		case <-changed:
		default:
			t.Fatal("empty observation lost wakeup")
		}
		if err := sink.TakeApplications(t.Context()); err != nil {
			t.Fatal(err)
		}
		sink.Close()
		sink.Close()
		if err := sink.TakeApplications(t.Context()); !errors.Is(err, teasink.ErrApplicationDeliveryLost) {
			t.Fatalf("closed proof: %v", err)
		}
	})
}

func TestApplicationTakeWaitsForSendAndCallback(t *testing.T) {
	for _, callback := range []bool{false, true} {
		t.Run(map[bool]string{false: "send", true: "callback"}[callback], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				entered, release := make(chan struct{}), make(chan struct{})
				var token teasink.ApplicationDelivery
				sink := teasink.NewApplicationSink(func(msg tea.Msg) {
					token = msg.(teasink.ApplicationDelivery)
					if callback {
						token.Apply(func(tea.Msg) { close(entered); <-release })
					} else {
						close(entered)
						<-release
					}
				})
				t.Cleanup(sink.Close)
				sink.AfterEvents("owned")
				<-entered
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if err := sink.TakeApplications(ctx); !errors.Is(err, context.Canceled) {
					t.Fatalf("timeout: %v", err)
				}
				joined := make(chan error, 1)
				var wg sync.WaitGroup
				wg.Go(func() { joined <- sink.TakeApplications(t.Context()) })
				synctest.Wait()
				select {
				case <-joined:
					t.Fatal("take returned before owned callback/send")
				default:
				}
				close(release)
				wg.Wait()
				if err := <-joined; err != nil {
					t.Fatal(err)
				}
				next, ok, _ := sink.NextApplication()
				if callback && ok {
					t.Fatal("claimed callback failed to ACK after epoch change")
				}
				if !callback {
					if token.Apply(func(tea.Msg) { t.Fatal("revoked transport token") }) {
						t.Fatal("stale apply")
					}
					if !ok || !next.Apply(func(msg tea.Msg) {
						if msg != "owned" {
							t.Fatal(msg)
						}
					}) {
						t.Fatal("lost unclaimed payload")
					}
				}
			})
		})
	}
}

func TestApplicationPanicIsLostNotReplayed(t *testing.T) {
	sink := teasink.NewApplicationSink(nil)
	t.Cleanup(sink.Close)
	sink.AfterEvents("partially applied")
	if err := sink.TakeApplications(t.Context()); err != nil {
		t.Fatal(err)
	}
	next, ok, _ := sink.NextApplication()
	if !ok {
		t.Fatal("no payload")
	}
	func() {
		defer func() {
			if recover() != "preserved panic" {
				t.Error("panic changed or swallowed")
			}
		}()
		next.Apply(func(tea.Msg) { panic("preserved panic") })
	}()
	if err := sink.TakeApplications(t.Context()); !errors.Is(err, teasink.ErrApplicationDeliveryLost) {
		t.Fatal(err)
	}
	if _, ok, _ := sink.NextApplication(); ok {
		t.Fatal("replayed partial event")
	}
}

func TestApplicationNilWiringAndReentry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink := teasink.NewApplicationSink(nil)
		t.Cleanup(sink.Close)
		sink.AfterEvents("before wire")
		sent := make(chan teasink.ApplicationDelivery, 1)
		sink.SetSend(func(msg tea.Msg) { sent <- msg.(teasink.ApplicationDelivery) })
		first := <-sent
		if !first.Apply(func(msg tea.Msg) {
			if msg != "before wire" {
				t.Fatal(msg)
			}
			sink.AfterEvents("reentrant producer")
			sink.SetSend(nil)
		}) {
			t.Fatal("claim")
		}
		if err := sink.TakeApplications(t.Context()); err != nil {
			t.Fatal(err)
		}
		next, ok, _ := sink.NextApplication()
		if !ok || !next.Apply(func(msg tea.Msg) {
			if msg != "reentrant producer" {
				t.Fatal(msg)
			}
		}) {
			t.Fatal("reentrant event lost")
		}
	})
	plain := teasink.New(nil)
	t.Cleanup(plain.Close)
	plain.AfterEvents("legacy drop")
	if err := plain.TakeApplications(t.Context()); !errors.Is(err, teasink.ErrApplicationsUnavailable) {
		t.Fatal(err)
	}
	if _, ok, _ := plain.NextApplication(); ok {
		t.Fatal("plain proof")
	}
}

func TestApplicationBoundedCapacity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink := teasink.NewApplicationSink(nil)
		t.Cleanup(sink.Close)
		for range 4096 {
			sink.AfterEvents("bounded")
		}
		pushed := make(chan struct{})
		var wg sync.WaitGroup
		wg.Go(func() { sink.AfterEvents("last"); close(pushed) })
		synctest.Wait()
		select {
		case <-pushed:
			t.Fatal("FIFO capacity exceeded")
		default:
		}
		if err := sink.TakeApplications(t.Context()); err != nil {
			t.Fatal(err)
		}
		next, ok, _ := sink.NextApplication()
		if !ok || !next.Apply(func(tea.Msg) {}) {
			t.Fatal("cannot release capacity")
		}
		wg.Wait()
		if sink.Overflows() != 1 {
			t.Fatal("missing bounded backpressure")
		}
	})
}

func TestApplicationUnwiredDrainRequiresApplication(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink := teasink.NewApplicationSink(nil)
		t.Cleanup(sink.Close)
		sink.AfterEvents("not yet applied")
		drained := make(chan struct{})
		var wg sync.WaitGroup
		wg.Go(func() { sink.Drain(); close(drained) })
		synctest.Wait()
		select {
		case <-drained:
			t.Fatal("unwired transport reported application")
		default:
		}
		if err := sink.TakeApplications(t.Context()); err != nil {
			t.Fatal(err)
		}
		next, ok, _ := sink.NextApplication()
		if !ok || !next.Apply(func(tea.Msg) {}) {
			t.Fatal("payload missing")
		}
		sink.NextApplication() // consumes the ordered pump-only drain sentinel
		wg.Wait()
		sink.Close()
		sink.SetSend(func(tea.Msg) { t.Error("send after final close") })
		sink.Close()
	})
}

func TestApplicationForwardedHeadCountsInsideCapacity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sent := make(chan teasink.ApplicationDelivery, 1)
		sink := teasink.NewApplicationSink(func(msg tea.Msg) { sent <- msg.(teasink.ApplicationDelivery) })
		t.Cleanup(sink.Close)
		sink.AfterEvents("forwarded head")
		old := <-sent
		for range 4095 {
			sink.AfterEvents("queued")
		}
		pushed := make(chan struct{})
		var wg sync.WaitGroup
		wg.Go(func() { sink.AfterEvents("last"); close(pushed) })
		synctest.Wait()
		select {
		case <-pushed:
			t.Fatal("forwarded head escaped bounded FIFO")
		default:
		}
		select {
		case <-sent:
			t.Fatal("second unacknowledged transport receipt")
		default:
		}
		if err := sink.TakeApplications(t.Context()); err != nil {
			t.Fatal(err)
		}
		if old.Apply(func(tea.Msg) { t.Error("stale token callback") }) {
			t.Fatal("transport token survived takeover")
		}
		next, ok, _ := sink.NextApplication()
		if !ok || !next.Apply(func(msg tea.Msg) {
			if msg != "forwarded head" {
				t.Error("head order changed")
			}
		}) {
			t.Fatal("missing retained head")
		}
		wg.Wait()
		if sink.Overflows() != 1 {
			t.Fatal("missing bounded backpressure")
		}
	})
}

func TestApplicationPreviewFencesAndForwardedFreeze(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sent := make(chan teasink.ApplicationDelivery, 1)
		sink := teasink.NewApplicationSink(func(msg tea.Msg) { sent <- msg.(teasink.ApplicationDelivery) })
		t.Cleanup(sink.Close)
		emit := func(text string) {
			sink.OnToolOutput(t.Context(), toolOutputEvent("task", 0, "call", text))
			sink.Flush()
		}
		emit("forwarded")
		first := <-sent
		emit("obsolete")
		emit("latest before content")
		sink.OnContent(t.Context(), runner.Content{Delta: "content fence"})
		sink.Flush()
		emit("latest before marker")
		sink.AfterEvents("marker fence")
		emit("latest before terminal")
		sink.OnToolCompleted(t.Context(), runner.ToolCompleted{TaskID: "task", ExecutionID: "call", ToolID: "tool", ToolName: "bash"})
		emit("late discarded")
		sink.OnToolStarted(t.Context(), runner.ToolStarted{TaskID: "task", ExecutionID: "call", ToolID: "tool", ToolName: "bash"})
		emit("new invocation")
		if err := sink.TakeApplications(t.Context()); err != nil {
			t.Fatal(err)
		}
		if first.Apply(func(tea.Msg) { t.Error("stale preview callback") }) {
			t.Fatal("stale preview applied")
		}
		var got []string
		for {
			next, ok, _ := sink.NextApplication()
			if !ok {
				break
			}
			if !next.Apply(func(msg tea.Msg) {
				switch e := msg.(type) {
				case teasink.ToolOutputMsg:
					got = append(got, e.Text)
				case teasink.ContentMsg:
					got = append(got, e.Delta)
				case teasink.ToolCompletedMsg:
					got = append(got, "completed")
				case teasink.ToolStartedMsg:
					got = append(got, "started")
				case string:
					got = append(got, e)
				default:
					t.Errorf("unexpected payload %T", msg)
				}
			}) {
				t.Fatal("preview application rejected")
			}
		}
		want := []string{"forwarded", "latest before content", "content fence", "latest before marker", "marker fence", "latest before terminal", "completed", "started", "new invocation"}
		if !slices.Equal(got, want) {
			t.Fatalf("preview application order = %q, want %q", got, want)
		}
	})
}

func TestApplicationClaimFreezesPreviewBeforeCallback(t *testing.T) {
	sink := teasink.NewApplicationSink(nil)
	t.Cleanup(sink.Close)
	emit := func(text string) {
		sink.OnToolOutput(t.Context(), toolOutputEvent("task", 0, "call", text))
		sink.Flush()
	}
	emit("claimed")
	if err := sink.TakeApplications(t.Context()); err != nil {
		t.Fatal(err)
	}
	next, ok, _ := sink.NextApplication()
	if !ok {
		t.Fatal("missing preview")
	}
	emit("later")
	if !next.Apply(func(msg tea.Msg) {
		if msg.(teasink.ToolOutputMsg).Text != "claimed" {
			t.Error("claim preview replaced")
		}
	}) {
		t.Fatal("claim rejected")
	}
	next, ok, _ = sink.NextApplication()
	if !ok || !next.Apply(func(msg tea.Msg) {
		if msg.(teasink.ToolOutputMsg).Text != "later" {
			t.Error("later preview lost")
		}
	}) {
		t.Fatal("later preview rejected")
	}
}

func TestApplicationTransportPanicLosesProof(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink := teasink.NewApplicationSink(func(tea.Msg) { panic("private-transport-canary") })
		t.Cleanup(sink.Close)
		sink.AfterEvents("owned")
		synctest.Wait()
		if err := sink.TakeApplications(t.Context()); !errors.Is(err, teasink.ErrApplicationDeliveryLost) {
			t.Fatalf("transport panic not lost: %v", err)
		}
		if _, ok, _ := sink.NextApplication(); ok {
			t.Fatal("replayed unknown transport outcome")
		}
	})
}

func TestApplicationCloseWaitsClaimedCallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink := teasink.NewApplicationSink(nil)
		t.Cleanup(sink.Close)
		sink.AfterEvents("claim")
		if err := sink.TakeApplications(t.Context()); err != nil {
			t.Fatal(err)
		}
		delivery, ok, _ := sink.NextApplication()
		if !ok {
			t.Fatal("missing payload")
		}
		entered, release, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var wg sync.WaitGroup
		wg.Go(func() {
			delivery.Apply(func(tea.Msg) {
				sink.AfterEvents("reentrant producer")
				if _, ok, _ := sink.NextApplication(); ok {
					t.Error("claimed callback admitted another claim")
				}
				close(entered)
				<-release
			})
		})
		<-entered
		wg.Go(func() { sink.Close(); close(closed) })
		synctest.Wait()
		select {
		case <-closed:
			t.Fatal("Close returned during application")
		default:
		}
		close(release)
		wg.Wait()
		if delivery.Apply(func(tea.Msg) { t.Error("closed token callback") }) {
			t.Fatal("closed claim replayed")
		}
	})
}
