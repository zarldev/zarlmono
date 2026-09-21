package tui_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func initialRewindChild(t *testing.T) (*beforeFixture, string, *engine.Settings) {
	t.Helper()
	f := newBeforeFixture(t)
	settings := engine.NewSettings(f.store, nil, nil, f.ws.Root())
	settings.Registry = nil
	f.ui.SetSettings(settings)
	f.provider.check = func(context.Context) {}
	settleRewindTurn(t, f, "first")
	previewRewindPrompt(f, 1)
	_, cmd := f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	driveBeforeCommand(f, cmd)
	id, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.GetSessionBranch(t.Context(), id); err != nil {
		t.Fatal("child not created", err)
	}
	return f, id, settings
}

func TestExactChildInterruptedBeforeSettlementRetainsPairedHead(t *testing.T) {
	f, id, settings := initialRewindChild(t)
	before := f.live.ContextSnapshot()
	// Execute the runner, apply all transcript events, but never apply the final
	// in-band settlement marker. No exact context/transcript pair was committed.
	driveBeforeCommand(f, f.ui.Submit("edited protected prompt"))
	f.sink.Drain()
	f.mu.Lock()
	events := f.events
	f.events = nil
	f.mu.Unlock()
	if len(events) < 2 {
		t.Fatal("missing turn events")
	}
	for _, event := range events[:len(events)-1] {
		f.ui.Update(event)
	}
	if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
		t.Fatal(err)
	}
	restarted := tui.New()
	restarted.SetSettings(settings)
	restarted.SetLiveRunner(f.live)
	restarted.SetLiveEventSink(f.sink)
	if err := restarted.ResumeSavedSession(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.live.ContextSnapshot(), before) || restarted.ComposerText() != "edited protected prompt" {
		t.Fatal("interrupted exact child lost its paired head or recovery draft")
	}
}

func TestCorruptExactResumeNeverFallsBackOrChangesActiveState(t *testing.T) {
	for _, scenario := range []string{"version", "revision", "watermark", "context", "target", "plan", "draft", "provenance checkpoint", "provenance checksum", "missing provenance"} {
		t.Run(scenario, func(t *testing.T) {
			f, id, _ := initialRewindChild(t)
			record, err := f.store.GetSession(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			var state map[string]any
			if err := json.Unmarshal(record.ContextJSON, &state); err != nil {
				t.Fatal(err)
			}
			want := rewind.ErrInvalid
			switch scenario {
			case "provenance checkpoint":
				state["initial_continuation"].(map[string]any)["checkpoint_id"] = "another"
			case "provenance checksum":
				state["initial_continuation"].(map[string]any)["checkpoint_checksum"] = "another"
			case "missing provenance":
				delete(state, "initial_continuation")
			case "version":
				state["rewind_resume_version"] = 99
			case "revision":
				state["revision"] = 99999
			case "watermark":
				state["event_watermark"] = 99999
			case "context":
				state["context"] = []any{map[string]any{"role": "tool", "tool_call_id": "missing", "content": "PRIVATE-canary"}}
			case "target":
				record.Model = "contradictory"
				want = rewind.ErrTarget
			case "plan":
				record.PlanJSON = []byte(`{"steps":`)
			case "draft":
				record.PendingJSON = []byte(`{"unsupported":"PRIVATE-canary"}`)
			}
			record.ContextJSON, err = json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.store.SaveSession(t.Context(), record); err != nil {
				t.Fatal(err)
			}
			if err := f.store.SetSetting(t.Context(), f.ws.Root(), "active_session", "previous"); err != nil {
				t.Fatal(err)
			}
			before := f.live.ContextSnapshot()
			if err := f.ui.ResumeSavedSession(t.Context(), id); !errors.Is(err, want) {
				t.Fatalf("resume = %v, want %v", err, want)
			}
			active, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
			if err != nil || active != "previous" || !reflect.DeepEqual(f.live.ContextSnapshot(), before) {
				t.Fatal("corrupt resume changed active state")
			}
		})
	}
}

func TestContinueNeverFallsBackFromActiveExactChild(t *testing.T) {
	for _, scenario := range []string{"corrupt envelope", "legacy downgrade", "provider unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			f, id, settings := initialRewindChild(t)
			before := f.live.ContextSnapshot()
			if scenario == "provider unavailable" {
				// The builtin route cannot be constructed without current credentials.
				settings.Registry = engine.NewSettings(f.store, nil, nil, f.ws.Root()).Registry
			} else {
				record, err := f.store.GetSession(t.Context(), id)
				if err != nil {
					t.Fatal(err)
				}
				record.ContextJSON = []byte(`{"rewind_resume_version":99}`)
				if scenario == "legacy downgrade" {
					record.ContextJSON = []byte(`[]`)
				}
				if err := f.store.SaveSession(t.Context(), record); err != nil {
					t.Fatal(err)
				}
			}
			restarted := tui.New()
			restarted.SetSettings(settings)
			restarted.SetLiveRunner(f.live)
			if err := restarted.ResumeLatestSavedSession(t.Context()); err == nil {
				t.Fatal("continue silently resumed another session")
			}
			active, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
			if err != nil || active != id || !reflect.DeepEqual(f.live.ContextSnapshot(), before) {
				t.Fatal("rejected continuation changed active state")
			}
			if !restarted.CanonicalThread().IsEmpty() || restarted.ComposerText() != "" {
				t.Fatal("rejected continuation published another session")
			}
		})
	}
}

func TestExactSettlementFailureAllowsFollowingMemoryOnlyTurn(t *testing.T) {
	for _, failedTurn := range []bool{false, true} {
		name := "completed"
		if failedTurn {
			name = "failed"
		}
		t.Run(name, func(t *testing.T) {
			f, id, _ := initialRewindChild(t)
			before, err := f.store.GetSessionResumeState(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			f.provider.check = func(context.Context) {
				calls++
				// BEFORE has committed. Refuse only the subsequent settlement.
				if calls == 1 {
					if _, err := f.store.DB().ExecContext(t.Context(), "CREATE TRIGGER reject_settlement BEFORE UPDATE ON sessions BEGIN SELECT RAISE(ABORT, 'injected settlement failure'); END"); err != nil {
						t.Fatal(err)
					}
				}
			}
			if failedTurn {
				f.provider.err = errors.New("terminal provider error")
			}
			settleRewindTurn(t, f, "unsaved turn")
			checkpoints, err := f.store.ListSessionCheckpoints(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			durable, err := f.store.GetSessionResumeState(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			settleRewindTurn(t, f, "continue in memory")
			after, err := f.store.ListSessionCheckpoints(t.Context(), id)
			if err != nil || calls != 2 || len(after) != len(checkpoints) {
				t.Fatalf("memory-only continuation blocked or created a checkpoint: calls=%d, checkpoints=%d, err=%v", calls, len(after), err)
			}
			if err := f.ui.SaveSession(t.Context()); err == nil {
				t.Fatal("unsaved exact history was admitted by an independent save")
			}
			stored, err := f.store.GetSessionResumeState(t.Context(), id)
			if err != nil || !reflect.DeepEqual(stored, durable) || !reflect.DeepEqual(stored.Transcript, before.Transcript) {
				t.Fatal("unsettled head overwrote the last durable exact pair", err)
			}
			if _, err := f.store.DB().ExecContext(t.Context(), "DROP TRIGGER reject_settlement"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
