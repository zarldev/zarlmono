package tui_test

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/options"
)

type beforeProvider struct {
	check   func(context.Context)
	err     error
	request func(llm.CompletionRequest)
}

func (beforeProvider) Name() string { return "openai" }
func (p beforeProvider) Complete(ctx context.Context, request llm.CompletionRequest) llm.CompletionStream {
	if p.request != nil {
		p.request(request)
	}
	p.check(ctx)
	return func(yield func(llm.CompletionChunk, error) bool) {
		if yield(llm.CompletionChunk{Content: "answer"}, nil) && p.err != nil {
			yield(llm.CompletionChunk{}, p.err)
		}
	}
}

func TestBeforeCheckpointPrecedesProviderDispatch(t *testing.T) {
	for _, startup := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "startup"}[startup], func(t *testing.T) {
			fixture := newBeforeFixture(t)
			fixture.ui.SetStartupReady(!startup)
			called := false
			fixture.provider.check = func(ctx context.Context) {
				called = true
				active, err := fixture.store.GetSettingExact(ctx, fixture.ws.Root(), "active_session")
				if err != nil {
					t.Fatal(err)
				}
				candidates, err := fixture.store.ListSessionCheckpoints(ctx, active)
				if err != nil || len(candidates) != 1 {
					t.Fatalf("BEFORE not durable: %v %v", candidates, err)
				}
				record, err := fixture.store.GetSessionCheckpoint(ctx, active, candidates[0].ID)
				if err != nil {
					t.Fatal(err)
				}
				checkpoint, err := rewind.Load(ctx, fixture.store, record)
				if err != nil {
					t.Fatal(err)
				}
				snapshot, err := checkpoint.Snapshot()
				if err != nil {
					t.Fatal(err)
				}
				if len(snapshot.Context) != 0 || snapshot.Transcript.Revision() != 0 || snapshot.Boundary.PromptText != "protected prompt" || snapshot.Boundary.PromptID == "" {
					t.Fatal("incorrect initial BEFORE boundary")
				}
				if strings.Contains(string(record.Payload), "private-api-canary") {
					t.Fatal("credential persisted")
				}
				if _, err := fixture.live.ReserveRuntime(); err == nil {
					t.Fatal("turn admission not held")
				}
			}
			cmd := fixture.ui.Submit("protected prompt")
			if startup {
				cmd = fixture.ui.ApplyStartupReady()
			}
			if called {
				t.Fatal("provider ran in Update")
			}
			if cmd == nil {
				t.Fatal("no dispatch command")
			}
			cmd()
			if !called {
				t.Fatal("provider not called")
			}
			fixture.applyEvents()
		})
	}
}

func TestBeforeCheckpointFailureRetainsPromptAndReleasesReservation(t *testing.T) {
	fixture := newBeforeFixture(t)
	fixture.provider.check = func(context.Context) { t.Fatal("provider dispatched without BEFORE") }
	fixture.ui.SetStartupReady(true)
	cmd := fixture.ui.Submit("keep this prompt")
	if err := fixture.store.Close(); err != nil {
		t.Fatal(err)
	}
	cmd()
	fixture.applyEvents()
	if fixture.ui.ComposerText() != "keep this prompt" {
		t.Fatal("prompt lost")
	}
	if !strings.Contains(fixture.ui.ToastText(), "not dispatched") {
		t.Fatal("missing failure notice")
	}
	reservation, err := fixture.live.ReserveRuntime()
	if err != nil {
		t.Fatal(err)
	}
	reservation.Release()
}

type beforeFixture struct {
	ui       *tui.UI
	live     *engine.LiveRunner
	store    *db.Store
	ws       code.Workspace
	provider *beforeProvider
	sink     *teasink.Sink
	mu       sync.Mutex
	events   []tea.Msg
}

func newBeforeFixture(t *testing.T, opts ...options.Option[engine.LiveRunner]) *beforeFixture {
	t.Helper()
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	f := &beforeFixture{ws: ws, store: store, provider: &beforeProvider{}}
	f.sink = teasink.New(func(msg tea.Msg) { f.mu.Lock(); f.events = append(f.events, msg); f.mu.Unlock() })
	t.Cleanup(f.sink.Close)
	opts = append(opts, engine.WithLiveSink(f.sink))
	f.live = engine.NewLiveRunner(f.provider, ws, "saved-model", opts...)
	f.live.SetProviderSpec(f.provider, engine.ProviderSpec{Name: "openai", Model: "saved-model", APIKey: "private-api-canary"})
	t.Cleanup(func() {
		if err := f.live.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	f.ui = tui.New()
	f.ui.SetLiveRunner(f.live)
	f.ui.SetLiveEventSink(f.sink)
	settings := engine.NewSettings(store, nil, nil, ws.Root())
	settings.Registry = nil // this fixture directly supplies the saved provider route
	f.ui.SetSettings(settings)
	return f
}

func (f *beforeFixture) applyEvents() {
	f.sink.Drain()
	f.mu.Lock()
	events := f.events
	f.events = nil
	f.mu.Unlock()
	for _, msg := range events {
		f.ui.Update(msg)
	}
}

func runBeforeCommands(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, child := range batch {
			runBeforeCommands(child)
		}
	}
}
