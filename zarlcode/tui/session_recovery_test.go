package tui_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/draft"
	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func failedRecoveryFixture(t *testing.T) (*beforeFixture, *int) {
	t.Helper()
	f := newBeforeFixture(t)
	f.ui.SetWorkspace(f.ws.Root(), "saved-model")
	calls := new(int)
	f.provider.check = func(ctx context.Context) {
		*calls++
		if *calls == 1 {
			if _, err := f.store.DB().ExecContext(ctx, `CREATE TRIGGER reject_recovery BEFORE UPDATE ON sessions BEGIN SELECT RAISE(ABORT, 'injected settlement failure'); END`); err != nil {
				t.Error(err)
			}
		}
	}
	settleRewindTurn(t, f, "failed visible turn")
	return f, calls
}

func removeRecoveryFailure(t *testing.T, f *beforeFixture) {
	t.Helper()
	if _, err := f.store.DB().ExecContext(t.Context(), "DROP TRIGGER reject_recovery"); err != nil {
		t.Fatal(err)
	}
}

func recoveryKey(ui *tui.UI, code rune) tea.Cmd {
	_, cmd := ui.Update(tea.KeyPressMsg{Code: code})
	return cmd
}

func openRecovery(ui *tui.UI) {
	ui.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	ui.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
}

func requireRecoveredContext(t *testing.T, f *beforeFixture, wantDraft string) {
	t.Helper()
	stored, err := f.store.GetSessionResumeState(t.Context(), f.ui.SessionIdentity())
	if err != nil {
		t.Fatal(err)
	}
	head, err := rewind.DecodeResume(stored.Session.ContextJSON)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(head.Context, f.live.ContextSnapshot()) || head.Revision != stored.Transcript.Revision {
		t.Fatal("retry did not durably pair accumulated context and transcript")
	}
	text, err := draft.Decode(stored.Session.PendingJSON)
	if err != nil || text != wantDraft {
		t.Fatalf("saved draft = %q, want %q: %v", text, wantDraft, err)
	}
}

func TestSessionRecoveryRetryAccumulatedTurnsAndDelayedAck(t *testing.T) {
	f, calls := failedRecoveryFixture(t)
	settleRewindTurn(t, f, "second memory-only turn")
	removeRecoveryFailure(t, f)
	f.ui.Update(tea.PasteMsg{Content: "captured draft"})
	openRecovery(f.ui)
	cmd := recoveryKey(f.ui, 'r')
	if cmd == nil {
		t.Fatal("missing retry command", f.ui.ToastText())
	}
	if duplicate := recoveryKey(f.ui, 'r'); duplicate != nil {
		t.Fatal("duplicate retry scheduled another write")
	}
	if _, err := f.live.ReserveRuntime(); !errors.Is(err, engine.ErrRuntimeBusy) {
		t.Fatalf("retry capture did not reserve runtime: %v", err)
	}
	priorSpec := f.ui.ActiveProviderSpec()
	f.ui.RepointProvider(f.provider, engine.ProviderSpec{Name: "openai", Model: "must-not-apply"}, 64000, nil)
	if f.ui.ActiveProviderSpec() != priorSpec || f.live.RunTarget().Model != "saved-model" {
		t.Fatal("pending provider result changed target during retry")
	}
	ack := cmd() // committed, not yet applied
	if _, err := f.live.ReserveRuntime(); !errors.Is(err, engine.ErrRuntimeBusy) {
		t.Fatalf("delayed acknowledgement released runtime: %v", err)
	}
	if *calls != 2 {
		t.Fatal("retry invoked provider")
	}
	requireRecoveredContext(t, f, "captured draft")
	if duplicate := recoveryKey(f.ui, 'r'); duplicate != nil {
		t.Fatal("committed-but-unapplied retry was scheduled again")
	}
	recoveryKey(f.ui, tea.KeyEscape)
	f.ui.Update(tea.PasteMsg{Content: " newer"})
	f.ui.Submit("must not dispatch")
	if !strings.Contains(f.ui.ToastText(), "acknowledgement") {
		t.Fatal("pending retry did not retain admission")
	}
	f.ui.Update(ack)
	f.ui.Update(ack) // stale identity cannot acknowledge another operation
	if f.ui.ComposerText() != "captured draft newer" {
		t.Fatal("retry acknowledgement replaced the composer")
	}
	requireRecoveredContext(t, f, "captured draft")
	f.ui.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !strings.Contains(f.ui.View().Content, "quit anyway") {
		t.Fatal("newer unsaved draft did not warn on quit")
	}
	if *calls != 2 {
		t.Fatal("recovery dispatched a turn")
	}
}

func TestSessionRecoveryRetryConflictPreservesFullRow(t *testing.T) {
	for _, column := range []string{"pending_json", "context_json", "plan_json"} {
		t.Run(column, func(t *testing.T) {
			f, calls := failedRecoveryFixture(t)
			removeRecoveryFailure(t, f)
			openRecovery(f.ui)
			cmd := recoveryKey(f.ui, 'r')
			if cmd == nil {
				t.Fatal("missing retry")
			}
			if _, err := f.store.DB().ExecContext(t.Context(), "UPDATE sessions SET "+column+" = ? WHERE id = ?", `{"foreign":"same revision"}`, f.ui.SessionIdentity()); err != nil {
				t.Fatal(err)
			}
			before, err := f.store.GetSessionResumeState(t.Context(), f.ui.SessionIdentity())
			if err != nil {
				t.Fatal(err)
			}
			f.ui.Update(cmd())
			after, err := f.store.GetSessionResumeState(t.Context(), f.ui.SessionIdentity())
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("conflicting full row or transcript was overwritten", err)
			}
			if !strings.Contains(f.ui.View().Content, "changed elsewhere") || *calls != 1 {
				t.Fatal("conflict not surfaced, or provider called")
			}
			if recoveryKey(f.ui, 'r') != nil {
				t.Fatal("conflicting retry remained enabled")
			}
		})
	}
}

func TestSessionRecoveryExportAndUnavailableRetryPreserveInput(t *testing.T) {
	for _, unsupported := range []bool{false, true} {
		t.Run(map[bool]string{false: "queued", true: "unsupported"}[unsupported], func(t *testing.T) {
			f := newBeforeFixture(t)
			f.ui.SetWorkspace(f.ws.Root(), "saved-model")
			if unsupported {
				p := nativeSettlementProvider{name: "openai", chunk: llm.CompletionChunk{Content: "visible unsupported answer", CompletedItems: []llm.ContinuationItem{{Provider: "openai", Format: "private-canary", Data: []byte(`{"private":"canary"}`)}}}}
				f.live.SetProviderSpec(p, engine.ProviderSpec{Name: "openai", Model: "saved-model"})
			} else {
				f.provider.check = func(ctx context.Context) {
					if _, err := f.store.DB().ExecContext(ctx, `CREATE TRIGGER reject_export BEFORE UPDATE ON sessions BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
						t.Error(err)
					}
				}
			}
			settleRewindTurn(t, f, "export failed visible history")
			before, err := f.store.GetSessionResumeState(t.Context(), f.ui.SessionIdentity())
			if err != nil {
				t.Fatal(err)
			}
			f.live.QueueAppend("queued input")
			f.ui.Update(tea.PasteMsg{Content: "independent draft"})
			openRecovery(f.ui)
			if recoveryKey(f.ui, 'r') != nil {
				t.Fatal("unavailable retry performed I/O")
			}
			// An export failure leaves the dialog usable; try again after removing
			// a local path obstruction. No durable session bytes are touched.
			parent := filepath.Join(f.ws.Root(), ".zarlcode")
			if err := os.MkdirAll(parent, 0o700); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(parent, "exports")
			if err := os.WriteFile(dir, []byte("obstruction"), 0o600); err != nil {
				t.Fatal(err)
			}
			f.ui.Update(recoveryKey(f.ui, 'e')())
			if err := os.Remove(dir); err != nil {
				t.Fatal(err)
			}
			f.ui.Update(recoveryKey(f.ui, 'e')())
			files, err := filepath.Glob(filepath.Join(dir, "*.md"))
			if err != nil || len(files) != 1 {
				t.Fatal("export not written", files, err)
			}
			body, err := os.ReadFile(files[0])
			if err != nil || !strings.Contains(string(body), "export failed visible history") || !strings.Contains(string(body), "answer") {
				t.Fatal("export omitted failed turn", err)
			}
			if f.live.QueueLen() != 1 || f.ui.ComposerText() != "independent draft" {
				t.Fatal("export/retry changed input")
			}
			view := f.ui.View().Content
			if !strings.Contains(view, "restart loses unsaved turns") || strings.Contains(view, "private-canary") {
				t.Fatal("export cleared warning or disclosed native payload")
			}
			after, err := f.store.GetSessionResumeState(t.Context(), f.ui.SessionIdentity())
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("recovery changed exact head", err)
			}
		})
	}
}

func TestSessionRecoveryRetryShutdownOwnership(t *testing.T) {
	for _, started := range []bool{false, true} {
		t.Run(map[bool]string{false: "unstarted", true: "committed unacknowledged"}[started], func(t *testing.T) {
			f, calls := failedRecoveryFixture(t)
			removeRecoveryFailure(t, f)
			openRecovery(f.ui)
			cmd := recoveryKey(f.ui, 'r')
			if cmd == nil {
				t.Fatal("missing retry")
			}
			if started {
				cmd()
			}
			if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
				t.Fatal(err)
			}
			if cmd() != nil || *calls != 1 {
				t.Fatal("late command repeated recovery or execution")
			}
			requireRecoveredContext(t, f, "")
			r, err := f.live.ReserveRuntime()
			if err != nil {
				t.Fatal("shutdown stranded reservation", err)
			}
			r.Release()
		})
	}
}

func TestSessionRecoveryQuitOverridesPreference(t *testing.T) {
	for _, confirm := range []string{"on", "off"} {
		t.Run(confirm, func(t *testing.T) {
			f, _ := failedRecoveryFixture(t)
			settings := engine.NewSettings(f.store, nil, nil, f.ws.Root())
			if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyConfirmQuit, confirm); err != nil {
				t.Fatal(err)
			}
			settings.Registry = nil
			f.ui.SetSettings(settings)
			f.ui.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
			_, cmd := f.ui.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
			if cmd != nil || !strings.Contains(f.ui.View().Content, "quit anyway") {
				t.Fatal("known loss risk skipped confirmation")
			}
			recoveryKey(f.ui, tea.KeyEnter)
			if strings.Contains(f.ui.View().Content, "quit anyway") {
				t.Fatal("Enter did not default to staying")
			}
			f.ui.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
			cmd = recoveryKey(f.ui, 'y')
			if cmd == nil {
				t.Fatal("missing explicit quit")
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Fatal("quit anyway did not quit")
			}
		})
	}
}

func TestSessionRecoveryFailedRetryAndStaleAcknowledgement(t *testing.T) {
	f, calls := failedRecoveryFixture(t)
	before, err := f.store.GetSessionResumeState(t.Context(), f.ui.SessionIdentity())
	if err != nil {
		t.Fatal(err)
	}
	openRecovery(f.ui)
	cmd := recoveryKey(f.ui, 'r')
	if cmd == nil {
		t.Fatal("missing first retry")
	}
	oldAck := cmd()
	f.ui.Update(oldAck)
	after, err := f.store.GetSessionResumeState(t.Context(), f.ui.SessionIdentity())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("failed retry changed head", err)
	}
	removeRecoveryFailure(t, f)
	cmd = recoveryKey(f.ui, 'r')
	if cmd == nil {
		t.Fatal("missing second retry")
	}
	f.ui.Update(oldAck)
	if _, err := f.live.ReserveRuntime(); !errors.Is(err, engine.ErrRuntimeBusy) {
		t.Fatal("stale ack released newer retry", err)
	}
	f.ui.Update(cmd())
	requireRecoveredContext(t, f, "")
	if *calls != 1 {
		t.Fatal("retry invoked provider")
	}
}

func TestSessionRecoveryCleanQuitAndDurableDraft(t *testing.T) {
	f := newBeforeFixture(t)
	settings := engine.NewSettings(f.store, nil, nil, f.ws.Root())
	if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyConfirmQuit, "off"); err != nil {
		t.Fatal(err)
	}
	settings.Registry = nil
	f.ui.SetSettings(settings)
	_, cmd := f.ui.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("clean quit unexpectedly warned")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("clean quit did not quit")
	}
	f.ui.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	f.ui.Update(tea.PasteMsg{Content: "draft only"})
	_, cmd = f.ui.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd != nil || !strings.Contains(f.ui.View().Content, "quit anyway") {
		t.Fatal("unsaved draft skipped warning")
	}
	recoveryKey(f.ui, tea.KeyEscape)
	// Drive an attributable draft save through its public debounce command.
	_, debounce := f.ui.Update(tea.PasteMsg{Content: " saved"})
	driveBeforeCommand(f, debounce)
	_, cmd = f.ui.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("durable draft incorrectly warned")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("durable draft did not quit")
	}
}

func TestSessionRecoveryRetryShutdownRejoinsAfterFastFlush(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, calls := failedRecoveryFixture(t)
		removeRecoveryFailure(t, f)
		openRecovery(f.ui)
		cmd := recoveryKey(f.ui, 'r')
		if cmd == nil {
			t.Fatal("missing retry")
		}
		conn, err := f.store.DB().Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		defer func() { conn.Close(); wg.Wait() }()
		wg.Go(func() { cmd() })
		synctest.Wait() // claimed retry is waiting for the only store connection
		fastCtx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := f.ui.FlushSessionPersistence(fastCtx); !errors.Is(err, context.Canceled) {
			t.Fatalf("fast flush = %v", err)
		}
		if r, err := f.live.ReserveRuntime(); !errors.Is(err, engine.ErrRuntimeBusy) {
			if r != nil {
				r.Release()
			}
			t.Fatalf("timed-out flush released pending retry: %v", err)
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
		if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
			t.Fatal(err)
		}
		wg.Wait()
		requireRecoveredContext(t, f, "")
		if *calls != 1 || cmd() != nil {
			t.Fatal("shutdown repeated execution")
		}
		r, err := f.live.ReserveRuntime()
		if err != nil {
			t.Fatal("shutdown stranded reservation", err)
		}
		r.Release()
	})
}

func TestSessionRecoveryQuitRechecksOpenConfirmation(t *testing.T) {
	f := newBeforeFixture(t)
	settings := engine.NewSettings(f.store, nil, nil, f.ws.Root())
	if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyConfirmQuit, "on"); err != nil {
		t.Fatal(err)
	}
	settings.Registry = nil
	f.ui.SetSettings(settings)
	f.ui.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	f.ui.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	// Input may arrive while a previously clean quit confirmation is open.
	f.live.QueueAppend("new queued input")
	if cmd := recoveryKey(f.ui, 'y'); cmd != nil || !strings.Contains(f.ui.View().Content, "quit anyway") {
		t.Fatal("old confirmation skipped newly known loss risk")
	}
	recoveryKey(f.ui, tea.KeyEnter)
	if f.live.QueueLen() != 1 {
		t.Fatal("staying changed queued input")
	}
}

func TestSessionRecoveryStartupFailureQuitWarns(t *testing.T) {
	f, _ := failedRecoveryFixture(t)
	f.ui.SetStartupFailure(f.ws.Root(), "Restart required", "Retained live work")
	f.ui.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	if cmd := recoveryKey(f.ui, tea.KeyEnter); cmd != nil || !strings.Contains(f.ui.View().Content, "quit anyway") {
		t.Fatal("startup failure quit skipped known loss risk")
	}
}

func TestSessionRecoveryRetryRestoresIntoFreshRunner(t *testing.T) {
	f, calls := failedRecoveryFixture(t)
	settleRewindTurn(t, f, "another completed memory-only turn")
	removeRecoveryFailure(t, f)
	f.ui.Update(tea.PasteMsg{Content: "draft for restart"})
	openRecovery(f.ui)
	cmd := recoveryKey(f.ui, 'r')
	if cmd == nil {
		t.Fatal("missing retry")
	}
	f.ui.Update(cmd())

	live := engine.NewLiveRunner(f.provider, f.ws, "saved-model")
	t.Cleanup(func() {
		if err := live.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	live.SetProviderSpec(f.provider, engine.ProviderSpec{Name: "openai", Model: "saved-model"})
	settings := engine.NewSettings(f.store, nil, nil, f.ws.Root())
	settings.Registry = nil
	restarted := tui.New()
	restarted.SetSettings(settings)
	restarted.SetLiveRunner(live)
	if err := restarted.ResumeSavedSession(t.Context(), f.ui.SessionIdentity()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(live.ContextSnapshot(), f.live.ContextSnapshot()) || restarted.ComposerText() != "draft for restart" {
		t.Fatal("strict resume lost recovered context or draft")
	}
	if *calls != 2 {
		t.Fatal("retry/resume invoked provider")
	}
}
