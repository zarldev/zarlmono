package tui_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
)

func TestBeforeCheckpointAllowsOtherConversationSelection(t *testing.T) {
	for _, timing := range []string{"before submit", "queued save"} {
		t.Run(timing, func(t *testing.T) {
			f := newBeforeFixture(t)
			f.ui.SetStartupReady(true)
			if err := f.store.SetSetting(t.Context(), f.ws.Root(), "active_session", "original"); err != nil {
				t.Fatal(err)
			}
			f.ui.StartFreshSession("") // capture original at the source transition
			called := false
			f.provider.check = func(context.Context) { called = true }
			changeActive := func() {
				t.Helper()
				if err := f.store.SetSetting(t.Context(), f.ws.Root(), "active_session", "other-process"); err != nil {
					t.Fatal(err)
				}
			}
			if timing == "before submit" {
				changeActive()
			}
			cmd := f.ui.Submit("retain this prompt")
			if timing == "queued save" {
				changeActive()
			}
			cmd()
			f.applyEvents()
			if !called {
				t.Fatal("another conversation's selection blocked dispatch")
			}
			active, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
			if err != nil || active == "other-process" {
				t.Fatalf("new conversation not activated: %q, %v", active, err)
			}
			if _, err := f.store.GetSessionTranscript(t.Context(), active); err != nil {
				t.Fatalf("dispatched without durable source: %v", err)
			}
			r, err := f.live.ReserveRuntime()
			if err != nil {
				t.Fatal(err)
			}
			r.Release()
		})
	}
}

func TestBeforeCheckpointFreshSelectionCanReplaceObservedActive(t *testing.T) {
	f := newBeforeFixture(t)
	f.ui.SetStartupReady(true)
	if err := f.store.SetSetting(t.Context(), f.ws.Root(), "active_session", "previous"); err != nil {
		t.Fatal(err)
	}
	f.ui.StartFreshSession("")
	called := false
	f.provider.check = func(ctx context.Context) {
		called = true
		active, err := f.store.GetSettingExact(ctx, f.ws.Root(), "active_session")
		if err != nil || active == "previous" {
			t.Fatalf("fresh selection not activated: %q, %v", active, err)
		}
		if _, err := f.store.GetSessionTranscript(ctx, active); errors.Is(err, db.ErrNotFound) {
			t.Fatal("dispatched without durable source")
		}
	}
	f.ui.Submit("fresh prompt")()
	if !called {
		t.Fatal("unchanged observed active selection rejected")
	}
}
