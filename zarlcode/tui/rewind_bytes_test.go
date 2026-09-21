package tui_test

import (
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestOpaqueContextSurvivesSettlementBranchAndRestart(t *testing.T) {
	f := newBeforeFixture(t)
	settings := engine.NewSettings(f.store, nil, nil, f.ws.Root())
	settings.Registry = nil // fixture supplies its provider directly
	f.ui.SetSettings(settings)
	const text = "raw\xff\x00"
	f.live.SetProviderSpec(nativeSettlementProvider{name: "openai", chunk: llm.CompletionChunk{Content: text}},
		engine.ProviderSpec{Name: "openai", Model: "saved-model"})
	settleRewindTurn(t, f, "first")
	historical := f.live.ContextSnapshot()
	if len(historical) == 0 || historical[len(historical)-1].Content != text {
		t.Fatalf("live context lost opaque output: %s", f.ui.ToastText())
	}
	stored, err := f.store.GetSessionResumeState(t.Context(), f.ui.SessionIdentity())
	if err != nil {
		t.Fatal(err)
	}
	head, err := rewind.DecodeResume(stored.Session.ContextJSON)
	if err != nil || !reflect.DeepEqual(head.Context, historical) {
		t.Fatalf("settlement lost opaque output: %v", err)
	}
	settleRewindTurn(t, f, "second")
	sourceID := f.ui.SessionIdentity()
	previewRewindPrompt(f, 1)
	_, apply := f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	driveBeforeCommand(f, apply)
	childID := f.ui.SessionIdentity()
	if childID == sourceID {
		t.Fatalf("branch not activated: %s", f.ui.ToastText())
	}
	want := append(llm.CloneMessages(historical), llm.Message{Role: llm.RoleUser, Content: rewind.FilesUnchangedNotice})
	if !reflect.DeepEqual(f.live.ContextSnapshot(), want) {
		t.Fatal("activation lost opaque output")
	}
	restarted := tui.New()
	restarted.SetLiveRunner(f.live)
	restarted.SetLiveEventSink(f.sink)
	restarted.SetSettings(settings)
	if err := restarted.ResumeSavedSession(t.Context(), childID); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.live.ContextSnapshot(), want) || restarted.ComposerText() != "second" {
		t.Fatal("restart lost opaque context or prompt prefill")
	}
}
