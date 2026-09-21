package tui_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/draft"
	"github.com/zarldev/zarlmono/zarlcode/engine"
)

func TestBeforeReservationCoversPersistenceQueueWait(t *testing.T) {
	f := newBeforeFixture(t)
	f.ui.SetStartupReady(true)
	_, debounce := f.ui.Update(tea.PasteMsg{Content: "draft before protected input"})
	_, prior := f.ui.Update(debounce())
	f.provider.check = func(context.Context) { t.Error("shutdown dispatched queued BEFORE") }
	f.ui.Submit("retained")
	if r, err := f.live.ReserveRuntime(); !errors.Is(err, engine.ErrRuntimeBusy) {
		if r != nil {
			r.Release()
		}
		t.Fatalf("queued BEFORE admission: %v", err)
	}
	f.live.SetModel("must-not-apply")
	if f.live.RunTarget().Model != "saved-model" {
		t.Error("target changed while FIFO pending")
	}
	// Complete the older write, but do not deliver its message and start BEFORE.
	prior()
	if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, err := f.live.ReserveRuntime()
	if err != nil {
		t.Fatal("shutdown stranded reservation", err)
	}
	r.Release()
}

func TestShutdownClaimsNeverStartedBeforeCommand(t *testing.T) {
	f := newBeforeFixture(t)
	f.ui.SetStartupReady(true)
	f.provider.check = func(context.Context) { t.Error("shutdown dispatched BEFORE") }
	cmd := f.ui.Submit("retained")
	if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, err := f.live.ReserveRuntime()
	if err != nil {
		t.Fatal("shutdown stranded reservation", err)
	}
	r.Release()
	id, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := f.store.GetSession(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	text, err := draft.Decode(saved.PendingJSON)
	if err != nil || text != "retained" {
		t.Fatalf("recovery draft %q: %v", text, err)
	}
	checkpoints, err := f.store.ListSessionCheckpoints(t.Context(), id)
	if err != nil || len(checkpoints) != 1 {
		t.Fatalf("BEFORE checkpoints %d: %v", len(checkpoints), err)
	}
	// A command delivered late cannot run, publish to the sink, or close twice.
	if msg := cmd(); msg != nil {
		t.Fatalf("late command returned %T", msg)
	}
	if msg := cmd(); msg != nil {
		t.Fatalf("repeated late command returned %T", msg)
	}
}

func TestShutdownCancelsBeforeCommandPriorToDispatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newBeforeFixture(t)
		f.ui.SetStartupReady(true)
		f.provider.check = func(context.Context) { t.Error("shutdown dispatched BEFORE") }
		cmd := f.ui.Submit("retained")
		var wg sync.WaitGroup
		wg.Go(func() {
			// Flush cancels the command and waits for its completion channel.
			_ = f.ui.FlushSessionPersistence(t.Context())
		})
		synctest.Wait()
		cmd()
		wg.Wait()
		r, err := f.live.ReserveRuntime()
		if err != nil {
			t.Fatal("shutdown stranded reservation", err)
		}
		r.Release()
	})
}

func TestShutdownWaitsForBeforeSinkBackpressure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newBeforeFixture(t)
		f.ui.SetStartupReady(true)
		f.provider.check = func(context.Context) { t.Error("shutdown dispatched BEFORE") }
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		f.sink.SetSend(func(tea.Msg) { once.Do(func() { close(entered); <-release }) })
		f.sink.AfterEvents("block pump")
		<-entered
		// Fill the documented bounded pump queue while its consumer is held.
		for range 4096 {
			f.sink.AfterEvents("queued")
		}
		cmd := f.ui.Submit("retained")
		flushed := make(chan struct{})
		var wg sync.WaitGroup
		// A started command that fails its save still owns sink publication.
		if err := f.store.Close(); err != nil {
			t.Fatal(err)
		}
		wg.Go(func() { cmd() })
		synctest.Wait() // the command is now blocked publishing its failure
		wg.Go(func() { _ = f.ui.FlushSessionPersistence(t.Context()); close(flushed) })
		synctest.Wait()
		select {
		case <-flushed:
			t.Error("flush returned while BEFORE still uses the sink")
		default:
		}
		close(release)
		wg.Wait()
	})
}

func TestShutdownJoinsStartedBeforeSaveAfterFastFlushDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newBeforeFixture(t)
		f.ui.SetStartupReady(true)
		f.provider.check = func(context.Context) { t.Error("shutdown dispatched BEFORE") }
		// Submit observes source content synchronously; hold the connection only
		// afterward so the background BEFORE save (not admission) is blocked.
		cmd := f.ui.Submit("retained during started save")
		conn, err := f.store.DB().Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		var wg sync.WaitGroup
		wg.Go(func() { cmd() })
		synctest.Wait() // executor claimed BEFORE and is waiting for the store connection
		fastCtx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := f.ui.FlushSessionPersistence(fastCtx); !errors.Is(err, context.Canceled) {
			t.Fatalf("fast flush = %v", err)
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
		// The lifecycle owner retries the join before closing DB or sink.
		if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
			t.Fatal(err)
		}
		wg.Wait()
		id, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
		if err != nil {
			t.Fatal(err)
		}
		saved, err := f.store.GetSession(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		text, err := draft.Decode(saved.PendingJSON)
		if err != nil || text != "retained during started save" {
			t.Fatalf("recovery draft %q: %v", text, err)
		}
		checkpoints, err := f.store.ListSessionCheckpoints(t.Context(), id)
		if err != nil || len(checkpoints) != 1 {
			t.Fatalf("checkpoint count = %d: %v", len(checkpoints), err)
		}
		r, err := f.live.ReserveRuntime()
		if err != nil {
			t.Fatal("shutdown stranded reservation", err)
		}
		r.Release()
	})
}
