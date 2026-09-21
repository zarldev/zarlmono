package tui_test

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

type settlementProvider struct{}

func (settlementProvider) Name() string { return "openai" }
func (settlementProvider) Complete(context.Context, llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		yield(llm.CompletionChunk{Content: "settled answer"}, nil)
	}
}

func TestLiveTurnSettlementRequiresAppliedEventsAndDurableSave(t *testing.T) {
	for _, failSave := range []bool{false, true} {
		name := "success"
		if failSave {
			name = "failed save"
		}
		t.Run(name, func(t *testing.T) {
			ws, err := code.NewWorkspace(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			var mu sync.Mutex
			var events []tea.Msg
			sink := teasink.New(func(msg tea.Msg) { mu.Lock(); events = append(events, msg); mu.Unlock() })
			defer sink.Close()
			live := engine.NewLiveRunner(settlementProvider{}, ws, "test-model", engine.WithLiveSink(sink))
			live.SetProviderSpec(settlementProvider{}, engine.ProviderSpec{Name: "openai", Model: "test-model"})
			defer func() {
				if err := live.Close(context.WithoutCancel(t.Context())); err != nil {
					t.Error(err)
				}
			}()
			ui := tui.New()
			ui.SetLiveRunner(live)
			ui.SetLiveEventSink(sink)
			ui.SetStartupReady(true)
			ui.SetSettings(engine.NewSettings(store, nil, nil, ws.Root()))
			cmd := ui.Submit("first prompt")
			if cmd == nil {
				t.Fatal("no turn command")
			}
			if msg := cmd(); msg != nil {
				t.Fatalf("completion bypassed sink: %T", msg)
			}
			sink.Drain()
			mu.Lock()
			messages := append([]tea.Msg(nil), events...)
			mu.Unlock()
			if len(messages) < 2 {
				t.Fatal("missing events")
			}
			marker := messages[len(messages)-1]
			for _, msg := range messages[:len(messages)-1] {
				ui.Update(msg)
			}
			live.QueueAppend("queued prompt")
			if live.QueueLen() != 1 {
				t.Fatal("queue lost before marker")
			}
			ui.Submit("too early")
			if !strings.Contains(ui.ToastText(), "settle and save") {
				t.Fatal("accepted a submission before settlement")
			}
			_, save := ui.Update(marker)
			if save == nil {
				t.Fatal("marker did not request a full save")
			}
			if live.QueueLen() != 1 {
				t.Fatal("queue promoted before durable save")
			}
			if failSave {
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			completion := save()
			_, next := ui.Update(completion)
			if live.QueueLen() != 1 {
				t.Fatal("queue consumed before the next BEFORE commit")
			}
			if !failSave {
				runBeforeCommands(next)
			}
			want := 0
			if failSave {
				want = 1
			}
			if live.QueueLen() != want {
				t.Fatalf("queue after save = %d, want %d", live.QueueLen(), want)
			}
			ui.Update(marker)
			if live.QueueLen() != want {
				t.Fatal("duplicate marker promoted input")
			}
		})
	}
}
