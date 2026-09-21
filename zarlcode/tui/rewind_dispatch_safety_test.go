package tui_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestBeforeCheckpointReservationCoversBlockedSave(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newBeforeFixture(t)
		f.ui.SetStartupReady(true)
		called := false
		f.provider.check = func(context.Context) { called = true }
		cmd := f.ui.Submit("protected")
		locked, release := make(chan struct{}), make(chan struct{})
		var wg sync.WaitGroup
		wg.Go(func() {
			if err := f.store.WithTx(t.Context(), func(*db.Store) error {
				close(locked)
				<-release
				return nil
			}); err != nil {
				t.Error(err)
			}
		})
		<-locked
		wg.Go(func() { cmd() })
		synctest.Wait()
		if called {
			t.Error("provider called while persistence blocked")
		}
		if r, err := f.live.ReserveRuntime(); err == nil {
			r.Release()
			t.Error("save did not own runtime reservation")
		}
		f.live.SetModel("must-not-apply")
		if f.live.RunTarget().Model != "saved-model" {
			t.Error("target changed while saving")
		}
		close(release)
		wg.Wait()
		if !called {
			t.Error("provider not called after commit")
		}
	})
}

func TestBeforeFailureRetainsAttachmentForRetry(t *testing.T) {
	f := newBeforeFixture(t)
	f.ui.SetStartupReady(true)
	path := filepath.Join(t.TempDir(), "attached.txt")
	if err := os.WriteFile(path, []byte("attachment bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.ui.AttachFile(path); err != nil {
		t.Fatal(err)
	}
	// A busy runtime rejects BEFORE without consuming attachment ownership.
	r, err := f.live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	f.provider.check = func(context.Context) { t.Fatal("dispatched through reservation") }
	cmd := f.ui.Submit("with attachment")
	f.ui.Update(cmd())
	f.applyEvents()
	r.Release()
	called := false
	f.provider.check = func(ctx context.Context) {
		called = true
		active, err := f.store.GetSettingExact(ctx, f.ws.Root(), "active_session")
		if err != nil {
			t.Fatal(err)
		}
		list, err := f.store.ListSessionCheckpoints(ctx, active)
		if err != nil || len(list) != 1 {
			t.Fatalf("checkpoints: %v", err)
		}
		record, err := f.store.GetSessionCheckpoint(ctx, active, list[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		checkpoint, err := rewind.Load(ctx, f.store, record)
		if err != nil {
			t.Fatal(err)
		}
		state, err := checkpoint.Snapshot()
		if err != nil || !state.Boundary.HasAttachments {
			t.Fatal("attachment metadata lost", err)
		}
	}
	f.ui.Submit(f.ui.ComposerText())()
	if !called {
		t.Fatal("retry did not dispatch")
	}
	context := f.live.ContextSnapshot()
	found := false
	for _, msg := range context {
		for _, part := range msg.Parts {
			if part.Type == llm.ContentTypeText && part.Text != "" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("attachment bytes lost")
	}
}

func TestQueuedPromotionCreatesItsOwnBeforeCheckpoint(t *testing.T) {
	f := newBeforeFixture(t)
	f.ui.SetStartupReady(true)
	calls := 0
	f.provider.check = func(ctx context.Context) {
		calls++
		active, err := f.store.GetSettingExact(ctx, f.ws.Root(), "active_session")
		if err != nil {
			t.Fatal(err)
		}
		list, err := f.store.ListSessionCheckpoints(ctx, active)
		if err != nil || len(list) != calls {
			t.Fatalf("call %d checkpoint count %d: %v", calls, len(list), err)
		}
	}
	f.ui.Submit("first")()
	f.sink.Drain()
	f.mu.Lock()
	events := f.events
	f.events = nil
	f.mu.Unlock()
	var commands []tea.Cmd
	for _, msg := range events {
		_, cmd := f.ui.Update(msg)
		commands = append(commands, cmd)
		if _, started := msg.(teasink.ConversationStartedMsg); started {
			f.ui.Submit("queued top-level prompt")
		}
	}
	if f.live.QueueLen() != 1 || calls != 1 {
		t.Fatal("promoted before settlement save")
	}
	for _, cmd := range commands {
		driveBeforeCommand(f, cmd)
	}
	if calls != 2 || f.live.QueueLen() != 0 {
		t.Fatalf("calls=%d queue=%d", calls, f.live.QueueLen())
	}
}

func driveBeforeCommand(f *beforeFixture, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, child := range batch {
			driveBeforeCommand(f, child)
		}
		return
	}
	if msg != nil {
		_, next := f.ui.Update(msg)
		driveBeforeCommand(f, next)
	}
}

// Keep engine busy errors in this fixture tied to the public admission contract.
var _ = engine.ErrRuntimeBusy
