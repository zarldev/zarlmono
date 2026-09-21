package tui_test

import (
	"context"
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestLiveTurnSettlementFailureShutdownPreservesDurableHead(t *testing.T) {
	for _, stage := range []string{"unstarted save", "undelivered failure", "applied failure"} {
		t.Run(stage, func(t *testing.T) {
			f := newBeforeFixture(t)
			f.provider.check = func(ctx context.Context) {
				if _, err := f.store.DB().ExecContext(ctx, `CREATE TRIGGER reject_settlement BEFORE UPDATE ON sessions BEGIN SELECT RAISE(ABORT, 'injected settlement failure'); END`); err != nil {
					t.Error(err)
				}
			}
			driveBeforeCommand(f, f.ui.Submit("unsaved turn"))
			f.sink.Drain()
			f.mu.Lock()
			events := f.events
			f.events = nil
			f.mu.Unlock()
			if len(events) == 0 {
				t.Fatal("missing turn events")
			}
			var save tea.Cmd
			for _, event := range events {
				_, save = f.ui.Update(event)
			}
			if save == nil {
				t.Fatal("missing settlement command")
			}
			before, err := f.store.GetSessionResumeState(t.Context(), f.ui.SessionIdentity())
			if err != nil {
				t.Fatal(err)
			}
			if stage != "unstarted save" {
				completion := save()
				if stage == "applied failure" {
					f.ui.Update(completion)
				}
			}
			if err := f.ui.FlushSessionPersistence(t.Context()); err == nil {
				t.Fatal("shutdown hid the settlement failure")
			}
			after, err := f.store.GetSessionResumeState(t.Context(), f.ui.SessionIdentity())
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("shutdown changed the last valid durable head", err)
			}
			if stage == "unstarted save" && save() != nil {
				t.Fatal("late settlement command ran after shutdown claimed it")
			}
			reservation, err := f.live.ReserveRuntime()
			if err != nil {
				t.Fatal("settlement failure stranded runtime admission", err)
			}
			reservation.Release()
		})
	}
}
