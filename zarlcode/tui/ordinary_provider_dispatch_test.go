package tui_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/draft"
	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestOrdinaryProviderDurableDispatchAndResume(t *testing.T) {
	for _, spec := range []engine.ProviderSpec{
		{Name: "custom", Model: "test-model", BaseURL: "https://model.example.test/v1"},
		{Name: "openai", Model: "test-model", BaseURL: "https://model.example.test/v1"},
		{Name: "llamacpp", Model: "test-model"},
		{Name: "google", Model: "test-model"},
	} {
		t.Run(spec.Name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newBeforeFixture(t)
				f.ui.SetStartupReady(true)
				f.ui.RepointProvider(f.provider, spec, 64000, nil)
				calls := 0
				f.provider.check = func(ctx context.Context) {
					calls++
					id, err := f.store.GetSettingExact(ctx, f.ws.Root(), "active_session")
					if err != nil {
						t.Fatal(err)
					}
					record, err := f.store.GetSession(ctx, id)
					if err != nil {
						t.Fatal(err)
					}
					pending, err := draft.Decode(record.PendingJSON)
					if err != nil || pending == "" {
						t.Fatalf("input not durable before dispatch: %v", err)
					}
				}
				settleRewindTurn(t, f, "first")
				id := f.ui.SessionIdentity()
				f.live.RestoreContext(nil)
				if err := f.ui.ResumeSavedSession(t.Context(), id); err != nil {
					t.Fatal(err)
				}
				settleRewindTurn(t, f, "second")
				if calls != 2 {
					t.Fatalf("provider calls = %d, want 2", calls)
				}
				record, err := f.store.GetSession(t.Context(), id)
				if err != nil {
					t.Fatal(err)
				}
				if record.Provider != spec.Name || record.Model != spec.Model {
					t.Fatalf("ordinary save changed target: %s/%s", record.Provider, record.Model)
				}
				var messages []llm.Message
				if err := json.Unmarshal(record.ContextJSON, &messages); err != nil || len(messages) < 4 {
					t.Fatalf("ordinary context not durably saved: %v (%s)", err, record.ContextJSON)
				}
				checkpoints, err := f.store.ListSessionCheckpoints(t.Context(), id)
				if err != nil || len(checkpoints) != 0 {
					t.Fatalf("unqualified provider advertised exact rewind: %d checkpoints, %v", len(checkpoints), err)
				}
			})
		})
	}
}

func TestOrdinaryProviderQueuedTurnWaitsForDurableSettlement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newBeforeFixture(t)
		f.ui.SetStartupReady(true)
		f.live.SetProviderSpec(f.provider, engine.ProviderSpec{Name: "custom", Model: "test-model"})
		calls := 0
		f.provider.check = func(context.Context) { calls++ }
		driveBeforeCommand(f, f.ui.Submit("first"))
		f.sink.Drain()
		f.mu.Lock()
		events := f.events
		f.events = nil
		f.mu.Unlock()
		if len(events) == 0 {
			t.Fatal("first turn not dispatched")
		}
		f.live.QueueAppend("queued second")
		for _, event := range events[:len(events)-1] {
			f.ui.Update(event)
		}
		if calls != 1 {
			t.Fatalf("queued turn dispatched before settlement: %d calls", calls)
		}
		_, cmd := f.ui.Update(events[len(events)-1])
		driveBeforeCommand(f, cmd)
		f.sink.Drain()
		f.mu.Lock()
		events = f.events
		f.events = nil
		f.mu.Unlock()
		for i, event := range events {
			_, cmd := f.ui.Update(event)
			if i == len(events)-1 {
				driveBeforeCommand(f, cmd)
			}
		}
		if calls != 2 {
			t.Fatalf("queued turn did not settle: calls=%d", calls)
		}
		reservation, err := f.live.ReserveRuntime()
		if err != nil {
			t.Fatal(err)
		}
		reservation.Release()
	})
}

func TestOrdinaryProviderForeignWriterBlocksDispatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newBeforeFixture(t)
		f.ui.SetStartupReady(true)
		f.live.SetProviderSpec(f.provider, engine.ProviderSpec{Name: "custom", Model: "test-model"})
		calls := 0
		f.provider.check = func(context.Context) { calls++ }
		settleRewindTurn(t, f, "first")
		id := f.ui.SessionIdentity()
		record, err := f.store.GetSession(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		record.PendingJSON, err = draft.Encode("foreign draft")
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.SaveSessionDraft(t.Context(), record); err != nil {
			t.Fatal(err)
		}
		foreign, err := f.store.SessionVersion(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		f.ui.Update(tea.PasteMsg{Content: "must remain recoverable"})
		driveBeforeCommand(f, f.ui.Submit("must remain recoverable"))
		if calls != 1 || !strings.Contains(f.ui.ComposerText(), "must remain recoverable") {
			t.Fatalf("source conflict lost input or dispatched: calls=%d draft=%q", calls, f.ui.ComposerText())
		}
		after, err := f.store.SessionVersion(t.Context(), id)
		if err != nil || after != foreign {
			t.Fatalf("dispatch overwrote competing writer: %v", err)
		}
	})
}

func TestOrdinaryProviderCompletedSaveRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newBeforeFixture(t)
		f.ui.SetStartupReady(true)
		f.live.SetProviderSpec(f.provider, engine.ProviderSpec{Name: "custom", Model: "test-model"})
		f.provider.check = func(ctx context.Context) {
			if _, err := f.store.DB().ExecContext(ctx, `CREATE TRIGGER reject_recovery BEFORE UPDATE ON sessions BEGIN SELECT RAISE(ABORT, 'injected settlement failure'); END`); err != nil {
				t.Error(err)
			}
		}
		settleRewindTurn(t, f, "retain completed turn")
		before, err := f.store.GetSession(t.Context(), f.ui.SessionIdentity())
		if err != nil {
			t.Fatal(err)
		}
		pending, err := draft.Decode(before.PendingJSON)
		if err != nil || pending != "retain completed turn" {
			t.Fatalf("failed settlement lost recovery input: %q, %v", pending, err)
		}
		f.provider.check = func(context.Context) {}
		settleRewindTurn(t, f, "memory-only continuation")
		removeRecoveryFailure(t, f)
		openRecovery(f.ui)
		cmd := recoveryKey(f.ui, 'r')
		if cmd == nil {
			t.Fatal("missing ordinary save retry command", f.ui.ToastText())
		}
		driveBeforeCommand(f, cmd)
		after, err := f.store.GetSession(t.Context(), f.ui.SessionIdentity())
		if err != nil {
			t.Fatal(err)
		}
		var messages []llm.Message
		if err := json.Unmarshal(after.ContextJSON, &messages); err != nil || len(messages) < 4 {
			t.Fatalf("retry did not persist context: %v", err)
		}
		settleRewindTurn(t, f, "can continue after retry")
	})
}
