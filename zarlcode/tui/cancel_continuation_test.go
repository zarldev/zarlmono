package tui_test

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

type interruptedProvider struct {
	started, cancelled, release chan struct{}
	calls                       atomic.Int32
}

func (*interruptedProvider) Name() string { return "test" }
func (p *interruptedProvider) Complete(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		if p.calls.Add(1) == 1 {
			close(p.started)
			<-ctx.Done()
			close(p.cancelled)
			<-p.release
			yield(llm.CompletionChunk{}, ctx.Err())
			return
		}
		yield(llm.CompletionChunk{Content: "continued"}, nil)
	}
}

func TestInterruptedTurnRetainsQueueAndAllowsContinuationWithBackgroundProcess(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	pm := code.NewProcessManager(ws)
	t.Cleanup(func() { pm.Close(context.WithoutCancel(t.Context())) })
	var mu sync.Mutex
	var events []tea.Msg
	sink := teasink.New(func(msg tea.Msg) { mu.Lock(); events = append(events, msg); mu.Unlock() })
	t.Cleanup(sink.Close)
	provider := &interruptedProvider{started: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}
	live := engine.NewLiveRunner(provider, ws, "test-model", engine.WithLiveSink(sink), engine.WithProcessManager(pm))
	live.SetProviderSpec(provider, engine.ProviderSpec{Name: "test", Model: "test-model"})
	t.Cleanup(func() {
		if err := live.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	ui := tui.New()
	ui.SetLiveRunner(live)
	ui.SetLiveEventSink(sink)
	ui.SetStartupReady(true)
	ui.SetSettings(engine.NewSettings(store, nil, nil, ws.Root()))
	ui.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	drain := func() []tea.Msg {
		sink.Drain()
		mu.Lock()
		defer mu.Unlock()
		batch := append([]tea.Msg(nil), events...)
		events = nil
		return batch
	}
	cmd := ui.Submit("first")
	if cmd == nil {
		t.Fatal("missing initial dispatch")
	}
	done := make(chan struct{})
	go func() { defer close(done); cmd() }()
	var release sync.Once
	t.Cleanup(func() { live.CancelTurn(); release.Do(func() { close(provider.release) }); <-done })
	<-provider.started
	for _, msg := range drain() {
		ui.Update(msg)
	}
	if _, err := pm.StartProcessContext(t.Context(), "sleep 60"); err != nil {
		t.Fatal(err)
	}
	if _, id := live.QueueAppend("queued follow-up"); id == 0 {
		t.Fatal("queue rejected")
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	<-provider.cancelled
	view := ansi.Strip(ui.View().Content)
	if !strings.Contains(view, "cancelling") {
		t.Errorf("missing cancellation progress: %s", view)
	}
	ui.Update(tea.PasteMsg{Content: "unsent draft"})
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if live.QueueLen() != 1 {
		t.Error("input submitted during cleanup instead of being retained")
	}
	if !strings.Contains(ui.ToastText(), "cancelling") {
		t.Errorf("unclear cleanup toast: %s", ui.ToastText())
	}
	release.Do(func() { close(provider.release) })
	<-done
	messages := drain()
	if len(messages) < 2 {
		t.Fatal("missing cancellation events and completion marker")
	}
	for _, msg := range messages[:len(messages)-1] {
		ui.Update(msg)
	}
	_, save := ui.Update(messages[len(messages)-1])
	if save == nil {
		t.Fatal("cancelled turn did not request durable save")
	}
	_, followup := ui.Update(save())
	if live.QueueLen() != 1 {
		t.Fatal("queued input lost during cancellation")
	}
	if strings.Contains(ui.ToastText(), "not quiescent") {
		t.Fatal(ui.ToastText())
	}
	if provider.calls.Load() != 1 {
		t.Fatal("provider ran again before explicit submission")
	}
	_ = followup // toast expiry is not a dispatch command
	view = ansi.Strip(ui.View().Content)
	if strings.Contains(view, "composer retained") || !strings.Contains(view, "Queued input ready") {
		t.Errorf("unclear queue status: %s", view)
	}
	_, next := ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if next == nil {
		t.Fatalf("explicit queue continuation rejected: %s", ui.ToastText())
	}
	runBeforeCommands(next)
	if provider.calls.Load() != 2 {
		t.Fatalf("continuation calls = %d, want 2: %s", provider.calls.Load(), ui.ToastText())
	}
	if live.QueueLen() != 0 {
		t.Fatalf("queue depth = %d, want 0", live.QueueLen())
	}
	if !strings.Contains(ansi.Strip(ui.View().Content), "unsent draft") {
		t.Fatal("continuation discarded the draft")
	}
}

func TestDurableTurnDispatchAllowsBackgroundProcess(t *testing.T) {
	ws, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	pm := code.NewProcessManager(ws)
	t.Cleanup(func() { pm.Close(context.WithoutCancel(t.Context())) })
	if _, err := pm.StartProcessContext(t.Context(), "sleep 60"); err != nil {
		t.Fatal(err)
	}
	sink := teasink.New(func(tea.Msg) {})
	t.Cleanup(sink.Close)
	live := engine.NewLiveRunner(settlementProvider{}, ws, "test-model", engine.WithLiveSink(sink), engine.WithProcessManager(pm))
	live.SetProviderSpec(settlementProvider{}, engine.ProviderSpec{Name: "openai", Model: "test-model"})
	t.Cleanup(func() {
		if err := live.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	ui := tui.New()
	ui.SetLiveRunner(live)
	ui.SetLiveEventSink(sink)
	ui.SetStartupReady(true)
	ui.SetSettings(engine.NewSettings(store, nil, nil, ws.Root()))
	cmd := ui.Submit("continue while server runs")
	if cmd == nil {
		t.Fatalf("dispatch rejected: %s", ui.ToastText())
	}
	runBeforeCommands(cmd)
	history := live.ContextSnapshot()
	if len(history) == 0 || history[len(history)-1].Content != "settled answer" {
		t.Fatalf("dispatch did not reach provider: %v", history)
	}
}
