package tui_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/draft"
	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/db"
)

// TestApplicationShutdownChildAccountingRuntimeBoundary retains the original
// review's real runner/SQLite assertion. Receipt fixtures opt in independently;
// legacy raw-message fixtures and the authenticated old82 overlay stay unchanged.
func TestApplicationShutdownChildAccountingRuntimeBoundary(t *testing.T) {
	for _, stage := range []string{"before root start", "after root terminal", "completion applied fullsave unstarted"} {
		t.Run(stage, func(t *testing.T) {
			f, late := configureLateChildFixture(t, newApplicationBeforeFixture(t), true)
			command := f.ui.Submit("quit at real runtime drain boundary")
			done := make(chan struct{})
			var wg sync.WaitGroup
			wg.Go(func() { defer close(done); runBeforeCommands(command) })
			t.Cleanup(wg.Wait)
			var held tea.Cmd
			if stage == "completion applied fullsave unstarted" {
				<-done
				drained := make(chan struct{})
				wg.Go(func() { f.sink.Drain(); close(drained) })
			drain:
				for {
					select {
					case delivery := <-f.applications:
						_, held = f.ui.Update(delivery)
					case <-drained:
						break drain
					case <-time.After(10 * time.Second):
						t.Fatal("receipt drain stalled")
					}
				}
				if held == nil {
					t.Fatal("missing held full save")
				}
			} else {
				for {
					var delivery teasink.ApplicationDelivery
					select {
					case delivery = <-f.applications:
					case <-time.After(10 * time.Second):
						t.Fatal("runtime did not publish")
					}
					if stage == "before root start" {
						<-done
						break
					}
					rootEnded := false
					if !delivery.Apply(func(msg tea.Msg) {
						f.ui.Update(msg)
						if end, ok := msg.(teasink.ConversationEndedMsg); ok && end.Depth == 0 {
							rootEnded = true
						}
					}) {
						t.Fatal("normal application rejected")
					}
					if rootEnded {
						break
					}
				}
			}
			id := f.ui.SessionIdentity()
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if err := f.ui.FlushSessionPersistence(ctx); err != nil {
				t.Fatal(err)
			}
			<-done
			if err := f.ui.FlushSessionPersistence(ctx); err != nil {
				t.Fatal("repeat flush", err)
			}
			var path string
			if err := f.store.DB().QueryRowContext(t.Context(), "SELECT file FROM pragma_database_list WHERE name = 'main'").Scan(&path); err != nil {
				t.Fatal(err)
			}
			reopened, err := db.Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			rows := durableChildren(t, reopened, id)
			if len(rows) != 1 || len(pendingChildren(t, f)) != 0 {
				t.Fatalf("successful shutdown dropped child: durable=%d pending=%d", len(rows), len(pendingChildren(t, f)))
			}
			if !late.childLate.Load() {
				t.Fatal("child did not settle after root terminal")
			}
			if held != nil {
				runBeforeCommands(held)
			}
		})
	}
}

func TestApplicationShutdownUnstartedBeforeNeverInvokes(t *testing.T) {
	f := newApplicationBeforeFixture(t)
	f.ui.SetStartupReady(true)
	var calls atomic.Int64
	f.provider.check = func(context.Context) { calls.Add(1) }
	command := f.ui.Submit("retain uninvoked recovery")
	if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
		t.Fatal(err)
	}
	runBeforeCommands(command)
	if calls.Load() != 0 {
		t.Fatal("shutdown dispatched uninvoked BEFORE")
	}
	reservation, err := f.live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	reservation.Release()
	record, err := f.store.GetSession(t.Context(), f.ui.SessionIdentity())
	if err != nil || len(record.PendingJSON) == 0 {
		t.Fatal("recovery not durable", err)
	}
}

func TestApplicationShutdownTakeTimeoutRetry(t *testing.T) {
	f, _ := configureLateChildFixture(t, newApplicationBeforeFixture(t), true)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.sink.SetSend(func(tea.Msg) { once.Do(func() { close(entered); <-release }) })
	command := f.ui.Submit("retain ownership through timeout")
	var wg sync.WaitGroup
	wg.Go(func() { runBeforeCommands(command) })
	<-entered
	wg.Wait() // real runtime returned; the delivered event is still unapplied
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := f.ui.FlushSessionPersistence(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled flush: %v", err)
	}
	close(release)
	ctx, cancel = context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := f.ui.FlushSessionPersistence(ctx); err != nil {
		t.Fatal("retry", err)
	}
	wg.Wait()
	if len(durableChildren(t, f.store, f.ui.SessionIdentity())) != 1 || len(pendingChildren(t, f)) != 0 {
		t.Fatal("timeout lost settlement")
	}
}

// drainApplicationFixture applies real FIFO receipts without recursively running
// animation commands. Only the final marker's command owns turn settlement.
func drainApplicationFixture(t *testing.T, f *beforeFixture) tea.Cmd {
	t.Helper()
	drained := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() { f.sink.Drain(); close(drained) })
	t.Cleanup(wg.Wait)
	var last tea.Cmd
	for {
		select {
		case delivery := <-f.applications:
			_, last = f.ui.Update(delivery)
		case <-drained:
			wg.Wait()
			return last
		case <-t.Context().Done():
			t.Fatal("application fixture cancelled")
		}
	}
}

func settleApplicationFixture(t *testing.T, f *beforeFixture, prompt string) {
	t.Helper()
	runBeforeCommands(f.ui.Submit(prompt))
	driveBeforeCommand(f, drainApplicationFixture(t, f))
}

func reopenApplicationStore(t *testing.T, f *beforeFixture) *db.Store {
	t.Helper()
	var path string
	if err := f.store.DB().QueryRowContext(t.Context(), "SELECT file FROM pragma_database_list WHERE name = 'main'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	reopened, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	})
	return reopened
}

func TestApplicationExactChildInterruptedSettlementIsDurable(t *testing.T) {
	f := newApplicationBeforeFixture(t)
	f.provider.check = func(context.Context) {}
	settleApplicationFixture(t, f, "first")
	previewRewindPrompt(f, 1)
	_, activate := f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	driveBeforeCommand(f, activate)
	id := f.ui.SessionIdentity()
	if _, err := f.store.GetSessionBranch(t.Context(), id); err != nil {
		t.Fatal("child not created", err)
	}
	before := f.live.ContextSnapshot()
	// The real run and its completion have returned, but no receipt is applied.
	runBeforeCommands(f.ui.Submit("edited protected prompt"))
	if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
		t.Fatal("repeat flush", err)
	}
	reopened := reopenApplicationStore(t, f)
	assertExactTranscriptPair(t, reopened, id)
	stored, err := reopened.GetSessionResumeState(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	head, err := rewind.DecodeResume(stored.Session.ContextJSON)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(before, head.Context) || !reflect.DeepEqual(f.live.ContextSnapshot(), head.Context) {
		t.Fatal("receipt shutdown did not commit the child's actual completed context")
	}
}

func TestApplicationShutdownRejectsUnknownPayload(t *testing.T) {
	f := newApplicationBeforeFixture(t)
	f.sink.AfterEvents(struct{ Private string }{"private-payload-canary"})
	err := f.ui.FlushSessionPersistence(t.Context())
	if err == nil || strings.Contains(err.Error(), "private-payload-canary") {
		t.Fatalf("unknown application outcome not safely rejected: %v", err)
	}
}

type missingTerminalApplicationSink struct{ *teasink.Sink }

func (*missingTerminalApplicationSink) OnConversationEnded(context.Context, runner.ConversationEnded) {
}

func TestApplicationShutdownMissingTerminalIsNotSuccess(t *testing.T) {
	f := newApplicationBeforeFixture(t)
	f.provider.check = func(context.Context) {}
	live := engine.NewLiveRunner(f.provider, f.ws, "saved-model", engine.WithLiveSink(&missingTerminalApplicationSink{f.sink}))
	live.SetProviderSpec(f.provider, engine.ProviderSpec{Name: "openai", Model: "saved-model"})
	t.Cleanup(func() {
		if err := live.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	f.live = live
	f.ui.SetLiveRunner(live)
	runBeforeCommands(f.ui.Submit("missing terminal"))
	if err := f.ui.FlushSessionPersistence(t.Context()); err == nil {
		t.Fatal("missing terminal claimed successful settlement")
	}
	if len(live.ContextSnapshot()) == 0 {
		t.Fatal("fixture did not execute a real turn")
	}
	reopened := reopenApplicationStore(t, f)
	stored, err := reopened.GetSession(t.Context(), f.ui.SessionIdentity())
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.PendingJSON) == 0 {
		t.Fatal("missing terminal lost BEFORE recovery")
	}
}

func TestApplicationShutdownForeignWriterAndDeleteGuards(t *testing.T) {
	for _, scenario := range []string{"writer after BEFORE", "delete", "delete conflict"} {
		t.Run(scenario, func(t *testing.T) {
			f := newApplicationBeforeFixture(t)
			f.provider.check = func(context.Context) {}
			settleApplicationFixture(t, f, "seed exact session")
			id := f.ui.SessionIdentity()
			if scenario == "writer after BEFORE" {
				runBeforeCommands(f.ui.Submit("unapplied completed turn"))
				if err := f.store.RenameSession(t.Context(), id, "foreign rename"); err != nil {
					t.Fatal(err)
				}
			} else {
				f.ui.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
				f.ui.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
				_, remove := f.ui.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
				if remove == nil {
					t.Fatal("delete was not queued")
				}
				if scenario == "delete conflict" {
					if err := f.store.RenameSession(t.Context(), id, "foreign rename"); err != nil {
						t.Fatal(err)
					}
				}
			}
			before, err := f.store.GetSessionResumeState(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			err = f.ui.FlushSessionPersistence(t.Context())
			reopened := reopenApplicationStore(t, f)
			if scenario == "delete" {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := reopened.GetSession(t.Context(), id); !errors.Is(err, db.ErrNotFound) {
					t.Fatalf("deleted session resurrected: %v", err)
				}
			} else {
				if !errors.Is(err, db.ErrCheckpointConflict) {
					t.Fatalf("foreign writer not rejected: %v", err)
				}
				after, err := reopened.GetSessionResumeState(t.Context(), id)
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatal("receipt shutdown overwrote foreign state", err)
				}
			}
		})
	}
}

func TestApplicationShutdownRejectsUnappliedSwarmActivation(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(map[bool]string{false: "shutdown cancels", true: "already stopping"}[stop], func(t *testing.T) {
			f := newApplicationBeforeFixture(t)
			var calls atomic.Int64
			f.provider.check = func(context.Context) { calls.Add(1) }
			settleApplicationFixture(t, f, "seed exact session")
			_ = f.ui.StartSwarm("recorded foreground goal")
			if !strings.Contains(f.ui.ToastText(), "swarm started") {
				t.Fatal(f.ui.ToastText())
			}
			// Hold the actual foreground worker's recorded-activation marker unapplied.
			select {
			case <-f.applications:
			case <-time.After(10 * time.Second):
				t.Fatal("recorded activation did not publish")
			}
			if stop {
				_ = f.ui.StopSwarm()
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if err := f.ui.FlushSessionPersistence(ctx); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 {
				t.Fatal("shutdown invoked unapplied recorded activation")
			}
			reservation, err := f.live.ReserveRuntime()
			if err != nil {
				t.Fatal("foreground worker not joined", err)
			}
			reservation.Release()
			if err := f.ui.FlushSessionPersistence(ctx); err != nil {
				t.Fatal("repeat flush", err)
			}
			reopened := reopenApplicationStore(t, f)
			assertExactTranscriptPair(t, reopened, f.ui.SessionIdentity())
		})
	}
}

func TestApplicationShutdownCannotDispatchQueuedStartupOrNewTurns(t *testing.T) {
	for _, startup := range []bool{false, true} {
		t.Run(map[bool]string{false: "queued", true: "startup"}[startup], func(t *testing.T) {
			f := newApplicationBeforeFixture(t)
			var calls atomic.Int64
			f.provider.check = func(context.Context) { calls.Add(1) }
			f.ui.SetStartupReady(!startup)
			cmd := f.ui.Submit("first")
			if !startup {
				runBeforeCommands(cmd)
				f.live.QueueAppend("queued must not dispatch")
			}
			if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
				t.Fatal(err)
			}
			runBeforeCommands(f.ui.ApplyStartupReady())
			runBeforeCommands(f.ui.Submit("after shutdown must not dispatch"))
			runBeforeCommands(f.ui.StartSwarm("after shutdown must not start"))
			expected := int64(1)
			if startup {
				expected = 0
			}
			if calls.Load() != expected {
				t.Fatalf("shutdown dispatched: calls=%d want=%d", calls.Load(), expected)
			}
			if !startup && f.live.QueueLen() != 1 {
				t.Fatal("shutdown consumed queued input")
			}
		})
	}
}

func TestApplicationShutdownCompletionMarkerQueuedDeliveredBlocked(t *testing.T) {
	for _, stage := range []string{"queued", "delivered", "blocked"} {
		t.Run(stage, func(t *testing.T) {
			f := newApplicationBeforeFixture(t)
			f.provider.check = func(context.Context) {}
			runBeforeCommands(f.ui.Submit("marker boundary"))
			var marker teasink.ApplicationDelivery
			entered, release := make(chan struct{}), make(chan struct{})
			var unblock sync.Once
			open := func() { unblock.Do(func() { close(release) }) }
			t.Cleanup(open)
			rootEnded := false
			for !rootEnded {
				delivery := <-f.applications
				if !delivery.Apply(func(payload tea.Msg) {
					f.ui.Update(payload)
					if end, ok := payload.(teasink.ConversationEndedMsg); ok && end.Depth == 0 {
						rootEnded = true
						switch stage {
						case "queued":
							f.sink.SetSend(nil)
						case "blocked":
							f.sink.SetSend(func(msg tea.Msg) { marker = msg.(teasink.ApplicationDelivery); close(entered); <-release })
						}
					}
				}) {
					t.Fatal("terminal application rejected")
				}
			}
			switch stage {
			case "delivered":
				marker = <-f.applications
			case "blocked":
				<-entered
				ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
				err := f.ui.FlushSessionPersistence(ctx)
				cancel()
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("blocked marker send not retained: %v", err)
				}
				open()
			}
			if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
				t.Fatal(err)
			}
			if marker.Apply(func(tea.Msg) { t.Error("stale marker callback") }) {
				t.Fatal("shutdown left marker replayable")
			}
			reopened := reopenApplicationStore(t, f)
			id := f.ui.SessionIdentity()
			assertExactTranscriptPair(t, reopened, id)
			stored, err := reopened.GetSession(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			head, err := rewind.DecodeResume(stored.ContextJSON)
			if err != nil || !reflect.DeepEqual(head.Context, f.live.ContextSnapshot()) {
				t.Fatal("completion marker did not settle committed context", err)
			}
		})
	}
}

func TestApplicationShutdownStartedUnadmittedBeforeRetainsRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newApplicationBeforeFixture(t)
		f.ui.SetStartupReady(true)
		var calls atomic.Int64
		f.provider.check = func(context.Context) { calls.Add(1) }
		cmd := f.ui.Submit("started save not admitted")
		conn, err := f.store.DB().Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		var wg sync.WaitGroup
		wg.Go(func() { runBeforeCommands(cmd) })
		synctest.Wait()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := f.ui.FlushSessionPersistence(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled flush: %v", err)
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
		// Its actual executor still publishes the real in-band result before done.
		if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
			t.Fatal(err)
		}
		wg.Wait()
		if calls.Load() != 0 {
			t.Fatal("unadmitted BEFORE invoked provider")
		}
		reopened := reopenApplicationStore(t, f)
		stored, err := reopened.GetSession(t.Context(), f.ui.SessionIdentity())
		if err != nil {
			t.Fatal(err)
		}
		text, err := draft.Decode(stored.PendingJSON)
		if err != nil || text != "started save not admitted" {
			t.Fatalf("recovery draft %q: %v", text, err)
		}
		runBeforeCommands(cmd)
		if calls.Load() != 0 {
			t.Fatal("late BEFORE command invoked provider")
		}
	})
}

// applicationSwarmTransport exercises the actual observation-capable receiving
// route entirely in process. No request reaches a network or paid provider.
type applicationSwarmTransport struct {
	calls            atomic.Int64
	cancelActivation bool
	started          chan struct{}
}

func (p *applicationSwarmTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	call := p.calls.Add(1)
	if call == 2 {
		close(p.started)
		if p.cancelActivation {
			<-req.Context().Done()
			return nil, req.Context().Err()
		}
	}
	body := "data: {\"id\":\"fixture\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"answer\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

func TestApplicationShutdownJoinsExecutedRecordedSwarm(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed receipts unapplied", true: "cancel and join"}[cancelled], func(t *testing.T) {
			f := newApplicationBeforeFixture(t)
			transport := &applicationSwarmTransport{cancelActivation: cancelled, started: make(chan struct{})}
			wire := openai.NewProvider("fixture-key", openai.WithModel("saved-model"), openai.WithHTTPClient(&http.Client{Transport: transport}))
			live := engine.NewLiveRunner(wire, f.ws, "saved-model", engine.WithLiveSink(f.sink), engine.WithReadOnlyTasks())
			live.SetProviderSpec(wire, engine.ProviderSpec{Name: "openai", Model: "saved-model"})
			t.Cleanup(func() {
				if err := live.Close(context.WithoutCancel(t.Context())); err != nil {
					t.Error(err)
				}
			})
			f.live = live
			f.ui.SetLiveRunner(live)
			settleApplicationFixture(t, f, "seed exact session")
			before := f.live.ContextSnapshot()
			_ = f.ui.StartSwarm("recorded foreground goal")
			if !strings.Contains(f.ui.ToastText(), "swarm started") {
				t.Fatal(f.ui.ToastText())
			}
			var activation teasink.ApplicationDelivery
			select {
			case activation = <-f.applications:
			case <-time.After(10 * time.Second):
				t.Fatal("activation did not publish")
			}
			_, command := f.ui.Update(activation)
			if command == nil {
				t.Fatal("activation did not construct BEFORE")
			}
			var wg sync.WaitGroup
			wg.Go(func() { runBeforeCommands(command) })
			<-transport.started
			if !cancelled {
				wg.Wait()
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if err := f.ui.FlushSessionPersistence(ctx); err != nil {
				t.Fatal(err)
			}
			wg.Wait()
			if transport.calls.Load() != 2 {
				t.Fatalf("activation calls = %d, want seed plus one activation", transport.calls.Load())
			}
			reservation, err := live.ReserveRuntime()
			if err != nil {
				t.Fatal("recorded foreground worker not joined", err)
			}
			reservation.Release()
			reopened := reopenApplicationStore(t, f)
			assertExactTranscriptPair(t, reopened, f.ui.SessionIdentity())
			stored, err := reopened.GetSession(t.Context(), f.ui.SessionIdentity())
			if err != nil {
				t.Fatal(err)
			}
			head, err := rewind.DecodeResume(stored.ContextJSON)
			if err != nil || !reflect.DeepEqual(head.Context, live.ContextSnapshot()) {
				t.Fatal("recorded activation context not settled", err)
			}
			if !cancelled && reflect.DeepEqual(head.Context, before) {
				t.Fatal("completed activation disappeared")
			}
			if err := f.ui.FlushSessionPersistence(ctx); err != nil {
				t.Fatal("repeat flush", err)
			}
		})
	}
}

func TestApplicationShutdownNonexactLateChildReopened(t *testing.T) {
	f, _ := configureLateChildFixture(t, newApplicationBeforeFixture(t), true)
	settleApplicationFixture(t, f, "establish recorded session")
	id := f.ui.SessionIdentity()
	record, err := f.store.GetSession(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	record.ContextJSON, err = json.Marshal(f.live.ContextSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.SaveActiveSession(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	if err := f.ui.ResumeSavedSession(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if err := f.live.RunRecordedTurn(t.Context(), "ordinary interrupted late child", nil, f.store, id); err != nil {
		t.Fatal(err)
	}
	if len(pendingChildren(t, f)) != 1 {
		t.Fatal("missing pending ordinary child")
	}
	if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
		t.Fatal(err)
	}
	reopened := reopenApplicationStore(t, f)
	if len(durableChildren(t, reopened, id)) != 2 || len(pendingChildren(t, f)) != 0 {
		t.Fatal("ordinary receipt shutdown lost/duplicated child")
	}
}

func TestApplicationShutdownStaleTokenCannotChangeRestoredUI(t *testing.T) {
	f := newApplicationBeforeFixture(t)
	f.provider.check = func(context.Context) {}
	runBeforeCommands(f.ui.Submit("completed before replacement"))
	old := <-f.applications
	if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
		t.Fatal(err)
	}
	reopened := reopenApplicationStore(t, f)
	id := f.ui.SessionIdentity()
	before, err := reopened.GetSessionResumeState(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	replacement := tui.New()
	settings := engine.NewSettings(reopened, nil, nil, f.ws.Root())
	settings.Registry = nil
	replacement.SetSettings(settings)
	replacement.SetLiveRunner(f.live)
	replacement.SetLiveEventSink(f.sink)
	if err := replacement.ResumeSavedSession(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	contextBefore := f.live.ContextSnapshot()
	if _, cmd := replacement.Update(old); cmd != nil {
		t.Fatal("stale sink token constructed effects in restored generation")
	}
	if !reflect.DeepEqual(contextBefore, f.live.ContextSnapshot()) || replacement.SessionIdentity() != id {
		t.Fatal("stale delivery changed restored session")
	}
	after, err := reopened.GetSessionResumeState(t.Context(), id)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("stale delivery changed durable session", err)
	}
}

type modeBeforeTerminalApplicationSink struct{ *teasink.Sink }

func (s *modeBeforeTerminalApplicationSink) OnConversationEnded(ctx context.Context, e runner.ConversationEnded) {
	if e.Depth == 0 {
		s.ModeChanged(engine.ModeChanged{TaskID: string(e.TaskID), Plan: true, Reason: "closing mode event"})
	}
	s.Sink.OnConversationEnded(ctx, e)
}

func TestApplicationShutdownSuppressesModeTargetConstruction(t *testing.T) {
	f := newApplicationBeforeFixture(t)
	f.provider.check = func(context.Context) {}
	live := engine.NewLiveRunner(f.provider, f.ws, "saved-model", engine.WithLiveSink(&modeBeforeTerminalApplicationSink{f.sink}))
	live.SetProviderSpec(f.provider, engine.ProviderSpec{Name: "openai", Model: "saved-model"})
	t.Cleanup(func() {
		if err := live.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	f.live = live
	f.ui.SetLiveRunner(live)
	before := live.RunTarget()
	runBeforeCommands(f.ui.Submit("mode event before terminal"))
	if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
		t.Fatal(err)
	}
	after := live.RunTarget()
	if before.Model != after.Model || before.Spec.Name != after.Spec.Name || before.Plan != after.Plan {
		t.Fatal("shutdown event constructed/applied a new runtime target")
	}
}

func TestApplicationShutdownUnstartedQueuedReservation(t *testing.T) {
	for _, application := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "receipts"}[application], func(t *testing.T) {
			f := newBeforeFixtureWithSink(t, application)
			var calls atomic.Int64
			f.provider.check = func(ctx context.Context) {
				calls.Add(1)
				f.live.QueueAppend("queued second")
				if _, err := f.store.DB().ExecContext(ctx, `CREATE TRIGGER correction_reject BEFORE UPDATE ON sessions BEGIN SELECT RAISE(ABORT, 'injected settlement failure'); END`); err != nil {
					t.Error(err)
				}
			}
			if application {
				settleApplicationFixture(t, f, "first")
			} else {
				settleRewindTurn(t, f, "first")
			}
			if !strings.Contains(f.ui.ToastText(), "you can continue") {
				t.Fatal("missing injected save failure", f.ui.ToastText())
			}
			if _, err := f.store.DB().ExecContext(t.Context(), "DROP TRIGGER correction_reject"); err != nil {
				t.Fatal(err)
			}
			f.ui.Update(tea.PasteMsg{Content: "composer remains"})
			held := f.ui.Submit("composer remains")
			if held == nil {
				t.Fatal("no queued dispatch command")
			}
			// The earlier failed save remains an error; shutdown must still
			// abandon its own uninvoked reservation and leave both inputs intact.
			if err := f.ui.FlushSessionPersistence(t.Context()); err == nil {
				t.Fatal("unsaved settlement reported success")
			}
			runBeforeCommands(held)
			if calls.Load() != 1 || f.live.QueueLen() != 1 || f.live.QueueSnapshot()[0].Message.Content != "queued second" {
				t.Fatal("shutdown dispatched or lost queued input")
			}
			if text := f.ui.ComposerText(); text != "composer remains" {
				t.Fatalf("queued input duplicated into composer: %q", text)
			}
			if err := f.live.Close(t.Context()); err != nil {
				t.Fatal("uninvoked queued reservation leaked", err)
			}
		})
	}
}

func TestApplicationShutdownAppliedUnstartedSwarmBefore(t *testing.T) {
	for _, application := range []bool{false, true} {
		for _, stopping := range []bool{false, true} {
			name := map[bool]string{false: "plain", true: "receipts"}[application] + "/" + map[bool]string{false: "cancel and retry", true: "already stopping"}[stopping]
			t.Run(name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					f := newBeforeFixtureWithSink(t, application)
					var calls atomic.Int64
					f.provider.check = func(context.Context) { calls.Add(1) }
					if application {
						settleApplicationFixture(t, f, "seed")
					} else {
						settleRewindTurn(t, f, "seed")
					}
					_ = f.ui.StartSwarm("recorded goal")
					if !strings.Contains(f.ui.ToastText(), "swarm started") {
						t.Fatal(f.ui.ToastText())
					}
					var activation tea.Msg
					if application {
						activation = <-f.applications
					} else {
						synctest.Wait()
						f.sink.Drain()
						f.mu.Lock()
						if len(f.events) != 1 {
							t.Fatalf("activation messages = %d", len(f.events))
						}
						activation, f.events = f.events[0], nil
						f.mu.Unlock()
					}
					_, held := f.ui.Update(activation)
					if held == nil {
						t.Fatal("no unstarted BEFORE command")
					}
					if stopping {
						_ = f.ui.StopSwarm()
					} else {
						ctx, cancel := context.WithCancel(t.Context())
						cancel()
						if err := f.ui.FlushSessionPersistence(ctx); !errors.Is(err, context.Canceled) {
							t.Fatal("cancelled flush", err)
						}
					}
					synctest.Wait()
					// Cancellation must not release the engine's callback-scoped
					// capability before the UI's recovery disposition is complete.
					if r, err := f.live.ReserveTurn(); !errors.Is(err, engine.ErrRuntimeBusy) {
						if r != nil {
							r.Release()
						}
						t.Fatal("callback relinquished unsettled reservation", err)
					}
					if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
						t.Fatal("shutdown recovery", err)
					}
					runBeforeCommands(held)
					if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
						t.Fatal("repeat flush", err)
					}
					if calls.Load() != 1 {
						t.Fatal("shutdown invoked held swarm BEFORE")
					}
					reopened := reopenApplicationStore(t, f)
					assertExactTranscriptPair(t, reopened, f.ui.SessionIdentity())
					if err := f.live.Close(t.Context()); err != nil {
						t.Fatal("swarm reservation not joined", err)
					}
				})
			})
		}
	}
}

func TestApplicationStopSwarmWaitsForRecoveryDisposition(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newApplicationBeforeFixture(t)
		f.provider.check = func(context.Context) {}
		settleApplicationFixture(t, f, "seed")
		_ = f.ui.StartSwarm("recorded goal")
		activation := <-f.applications
		_, held := f.ui.Update(activation)
		if held == nil {
			t.Fatal("no BEFORE command")
		}
		stopped := tui.SwarmStoppedForTest(f.ui)
		_ = f.ui.StopSwarm()
		synctest.Wait()
		select {
		case <-stopped:
			t.Fatal("Stop returned before recovery disposition")
		default:
		}
		driveBeforeCommand(f, held)
		// Ordinary UI application is the consumer here. Keep applying and
		// executing settlement commands while the stop owner drains its worker.
		for {
			synctest.Wait()
			select {
			case <-stopped:
				if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
					t.Fatal(err)
				}
				return
			case delivery := <-f.applications:
				_, cmd := f.ui.Update(delivery)
				driveBeforeCommand(f, cmd)
			}
		}
	})
}

func TestApplicationStopSwarmRevokesUnappliedMarker(t *testing.T) {
	for _, application := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "receipts"}[application], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newBeforeFixtureWithSink(t, application)
				var calls atomic.Int64
				f.provider.check = func(context.Context) { calls.Add(1) }
				if application {
					settleApplicationFixture(t, f, "seed")
				} else {
					settleRewindTurn(t, f, "seed")
				}
				_ = f.ui.StartSwarm("recorded goal")
				var marker tea.Msg
				if application {
					marker = <-f.applications
				} else {
					synctest.Wait()
					f.sink.Drain()
					f.mu.Lock()
					if len(f.events) != 1 {
						t.Fatalf("activation messages = %d", len(f.events))
					}
					marker, f.events = f.events[0], nil
					f.mu.Unlock()
				}
				stopped := tui.SwarmStoppedForTest(f.ui)
				_ = f.ui.StopSwarm()
				synctest.Wait()
				select {
				case <-stopped:
				default:
					t.Fatal("unapplied activation required a consumer to stop")
				}
				if _, cmd := f.ui.Update(marker); cmd != nil {
					t.Fatal("revoked activation constructed a BEFORE command")
				}
				if err := f.ui.FlushSessionPersistence(t.Context()); err != nil {
					t.Fatal(err)
				}
				if calls.Load() != 1 {
					t.Fatal("revoked activation invoked the provider")
				}
				if err := f.live.Close(t.Context()); err != nil {
					t.Fatal("revoked reservation not joined", err)
				}
			})
		})
	}
}

func TestApplicationShutdownRejectedSwarmPreparationNeedsNoCommand(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newApplicationBeforeFixture(t)
		f.provider.check = func(context.Context) {}
		settleApplicationFixture(t, f, "seed")
		// The UI still holds its exact-session identity, but the current runtime
		// no longer qualifies for exact checkpoint capture. Reject at preparation.
		f.live.SetProviderSpec(f.provider, engine.ProviderSpec{Name: "custom", Model: "saved-model"})
		_ = f.ui.StartSwarm("rejected recorded goal")
		activation := <-f.applications
		_, held := f.ui.Update(activation)
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		err := f.ui.FlushSessionPersistence(ctx)
		if errors.Is(err, context.DeadlineExceeded) {
			// Cleanup also makes the old-source sensitivity check leak-free.
			if held != nil {
				f.sink.AfterEvents(held())
			}
			_ = f.ui.FlushSessionPersistence(t.Context())
			t.Error("preparation rejection still depended on an unstarted command")
		}
		if !strings.Contains(f.ui.ToastText(), "swarm activation failed") {
			t.Fatal("preparation rejection was not applied", f.ui.ToastText())
		}
		if err := f.live.Close(t.Context()); err != nil {
			t.Fatal("rejected callback reservation not joined", err)
		}
	})
}
