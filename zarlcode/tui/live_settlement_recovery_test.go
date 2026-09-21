package tui_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/draft"
	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

type nativeSettlementProvider struct {
	name  string
	chunk llm.CompletionChunk
}

func (p nativeSettlementProvider) Name() string { return p.name }
func (p nativeSettlementProvider) Complete(context.Context, llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) { yield(p.chunk.Clone(), nil) }
}

func TestLiveTurnNativeReasoningSettlement(t *testing.T) {
	for _, name := range []string{"anthropic", "openai-codex"} {
		t.Run(name, func(t *testing.T) {
			f := newBeforeFixture(t)
			item := llm.ContinuationItem{Provider: name, Format: "responses.reasoning.v1", Kind: "reasoning", ID: "rs_1", OutputIndex: llm.OutputPosition(0), Data: []byte(`{"type":"reasoning","id":"rs_1","encrypted_content":"opaque-private-canary"}`)}
			if name == "anthropic" {
				item.Format, item.Kind = "content_block.v1", "thinking"
				item.Data = []byte(`{"type":"thinking","thinking":"Considering","signature":"signed-private-canary"}`)
			}
			provider := nativeSettlementProvider{name: name, chunk: llm.CompletionChunk{Content: "answer", Thinking: "Considering", ContentOutputIndex: llm.OutputPosition(1), CompletedItems: []llm.ContinuationItem{item}}}
			f.live.SetProviderSpec(provider, engine.ProviderSpec{Name: name, Model: "test-model"})
			for _, prompt := range []string{"first", "second"} {
				settleRewindTurn(t, f, prompt)
				stored, err := f.store.GetSessionResumeState(t.Context(), f.ui.SessionIdentity())
				if err != nil {
					t.Fatal(err)
				}
				resume, err := rewind.DecodeResume(stored.Session.ContextJSON)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(resume.Context, f.live.ContextSnapshot()) {
					t.Fatal("native reasoning head not durable")
				}
			}
		})
	}
}

func TestLiveTurnSettlementFailureAllowsNextPrompt(t *testing.T) {
	f := newBeforeFixture(t)
	calls := 0
	f.provider.check = func(ctx context.Context) {
		calls++
		if calls == 1 {
			if _, err := f.store.DB().ExecContext(ctx, `CREATE TRIGGER reject_settlement BEFORE UPDATE ON sessions BEGIN SELECT RAISE(ABORT, 'injected settlement failure'); END`); err != nil {
				t.Error(err)
			}
		}
	}
	settleRewindTurn(t, f, "first")
	if !strings.Contains(f.ui.ToastText(), "you can continue") {
		t.Fatal("save failure did not explain memory-only continuation")
	}
	if _, err := f.store.DB().ExecContext(t.Context(), "DROP TRIGGER reject_settlement"); err != nil {
		t.Fatal(err)
	}
	settleRewindTurn(t, f, "second")
	if calls != 2 {
		t.Fatal("save failure blocked the next provider turn")
	}
	stored, err := f.store.GetSessionResumeState(t.Context(), f.ui.SessionIdentity())
	if err != nil {
		t.Fatal(err)
	}
	resume, err := rewind.DecodeResume(stored.Session.ContextJSON)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resume.Context, f.live.ContextSnapshot()) {
		t.Fatal("subsequent successful save did not retain both turns exactly")
	}
	f.ui.Update(tea.WindowSizeMsg{Width: 160, Height: 32})
	if strings.Contains(f.ui.View().Content, "Not saved") {
		t.Fatal("successful full save left the persistent warning active")
	}
	settleRewindTurn(t, f, "third")
	if calls != 3 {
		t.Fatal("durable dispatch did not recover")
	}
}

func TestLiveTurnSettlementFailureKeepsQueueManuallyDispatchable(t *testing.T) {
	f := newBeforeFixture(t)
	calls := 0
	f.provider.check = func(ctx context.Context) {
		calls++
		if calls == 1 {
			f.live.QueueAppend("queued second")
			if _, err := f.store.DB().ExecContext(ctx, `CREATE TRIGGER reject_settlement BEFORE UPDATE ON sessions BEGIN SELECT RAISE(ABORT, 'injected settlement failure'); END`); err != nil {
				t.Error(err)
			}
		}
	}
	settleRewindTurn(t, f, "first")
	if calls != 1 || len(f.live.QueueSnapshot()) != 1 {
		t.Fatal("save failure implicitly dispatched or lost queued input")
	}
	if _, err := f.store.DB().ExecContext(t.Context(), "DROP TRIGGER reject_settlement"); err != nil {
		t.Fatal(err)
	}
	f.ui.Update(tea.PasteMsg{Content: "third draft"})
	settleRewindTurn(t, f, "third draft") // explicit submit sends already accepted queued input first
	if calls != 2 || len(f.live.QueueSnapshot()) != 0 || f.ui.ComposerText() != "third draft" {
		t.Fatal("memory-only queue dispatch lost or duplicated input")
	}
	stored, err := f.store.GetSessionResumeState(t.Context(), f.ui.SessionIdentity())
	if err != nil {
		t.Fatal(err)
	}
	resume, err := rewind.DecodeResume(stored.Session.ContextJSON)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resume.Context, f.live.ContextSnapshot()) {
		t.Fatal("queued recovery did not save both accumulated turns exactly")
	}
	var prompts []string
	for _, message := range resume.Context {
		if message.Role == llm.RoleUser {
			prompts = append(prompts, message.Content)
		}
	}
	if !reflect.DeepEqual(prompts, []string{"first", "queued second"}) {
		t.Fatalf("saved prompts = %q", prompts)
	}
	text, err := draft.Decode(stored.Session.PendingJSON)
	if err != nil || text != "third draft" {
		t.Fatalf("recovered composer draft = %q: %v", text, err)
	}
}

func TestLiveTurnSettlementEncodingFailureDoesNotBlock(t *testing.T) {
	f := newBeforeFixture(t)
	provider := nativeSettlementProvider{name: "openai", chunk: llm.CompletionChunk{Content: "answer", CompletedItems: []llm.ContinuationItem{{Provider: "openai", Format: "unknown-private-canary", Data: []byte(`{"private":"private-canary"}`)}}}}
	f.live.SetProviderSpec(provider, engine.ProviderSpec{Name: "openai", Model: "saved-model"})
	settleRewindTurn(t, f, "first")
	before, err := f.store.GetSessionResumeState(t.Context(), f.ui.SessionIdentity())
	if err != nil {
		t.Fatal(err)
	}
	for _, prompt := range []string{"second", "third"} {
		previous := len(f.live.ContextSnapshot())
		settleRewindTurn(t, f, prompt)
		if len(f.live.ContextSnapshot()) <= previous {
			t.Fatal("invalid exact head blocked live continuation")
		}
		if toast := f.ui.ToastText(); !strings.Contains(toast, "you can continue") || !strings.Contains(toast, "restart loses unsaved turns") || strings.Contains(toast, "private-canary") {
			t.Fatalf("unsafe or missing durability warning: %s", toast)
		}
		if err := f.ui.SaveSession(t.Context()); !errors.Is(err, rewind.ErrInvalid) {
			t.Fatalf("invalid state was saved at shutdown: %v", err)
		}
		if err := f.ui.FlushSessionPersistence(t.Context()); !errors.Is(err, rewind.ErrInvalid) {
			t.Fatalf("shutdown flush did not retain the unsaved state: %v", err)
		}
		after, err := f.store.GetSessionResumeState(t.Context(), f.ui.SessionIdentity())
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("memory-only continuation modified the last valid checkpoint", err)
		}
	}
	f.ui.SetSuccessToast("transient toast")
	f.ui.Update(tea.WindowSizeMsg{Width: 160, Height: 32})
	if !strings.Contains(f.ui.View().Content, "Not saved — checkpoint rejected") || !strings.Contains(f.ui.View().Content, "Ctrl+q: recovery") {
		t.Fatal("unsaved warning disappeared with a transient toast")
	}
}
