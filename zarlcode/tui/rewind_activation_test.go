package tui_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/draft"
	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func settleRewindTurn(t *testing.T, f *beforeFixture, prompt string) {
	t.Helper()
	driveBeforeCommand(f, f.ui.Submit(prompt))
	f.sink.Drain()
	f.mu.Lock()
	events := f.events
	f.events = nil
	f.mu.Unlock()
	if len(events) == 0 {
		t.Fatalf("no turn events: %s", f.ui.ToastText())
	}
	for i, event := range events {
		_, cmd := f.ui.Update(event)
		// The final in-band marker owns settlement. Animation commands from earlier
		// events must not be recursively driven while the turn is still marked live.
		if i == len(events)-1 {
			driveBeforeCommand(f, cmd)
		}
	}
}

func previewRewindPrompt(f *beforeFixture, promptsBack int) {
	f.ui.Update(tea.WindowSizeMsg{Width: 120, Height: 42})
	f.ui.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	f.ui.View() // initialize the reader's tail viewport before selection
	for range promptsBack {
		f.ui.Update(tea.KeyPressMsg{Code: '[', Text: "["})
	}
	f.ui.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	f.ui.View() // review the preview before confirming, as the terminal does
}

func TestRewindBranchRestartAndContinue(t *testing.T) {
	f := newBeforeFixture(t)
	settings := engine.NewSettings(f.store, nil, nil, f.ws.Root())
	settings.Registry = nil // fixture supplies its provider directly
	f.ui.SetSettings(settings)
	f.provider.check = func(context.Context) {}
	settleRewindTurn(t, f, "same prompt")
	historical := f.live.ContextSnapshot()
	settleRewindTurn(t, f, "same prompt")
	sourceID, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
	if err != nil {
		t.Fatal(err)
	}
	source, err := f.store.GetSessionResumeState(t.Context(), sourceID)
	if err != nil {
		t.Fatal(err)
	}
	// A draft typed after the turn must remain on the source, not replace prefill.
	f.ui.Update(tea.PasteMsg{Content: "original draft"})
	previewRewindPrompt(f, 1)
	if view := f.ui.View().Content; strings.Contains(view, "Unavailable:") {
		t.Fatal(view)
	}
	_, apply := f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	childID, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
	if err != nil || childID == sourceID {
		t.Fatalf("branch not activated: %v %s", err, f.ui.ToastText())
	}
	if f.ui.ComposerText() != "same prompt" {
		t.Fatal("selected prompt not prefilled")
	}
	want := append(llm.CloneMessages(historical), llm.Message{Role: llm.RoleUser, Content: rewind.FilesUnchangedNotice})
	if !reflect.DeepEqual(f.live.ContextSnapshot(), want) {
		t.Fatal("wrong repeated-prompt boundary restored")
	}
	after, err := f.store.GetSessionResumeState(t.Context(), sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(source.Transcript, after.Transcript) {
		t.Fatal("source canonical history changed")
	}
	text, err := draft.Decode(after.Session.PendingJSON)
	if err != nil || text != "original draft" {
		t.Fatalf("source draft: %q %v", text, err)
	}

	// Restart immediately after the atomic child commit, before its new notice
	// has been persisted by the returned command.
	restarted := tui.New()
	restarted.SetLiveRunner(f.live)
	restarted.SetLiveEventSink(f.sink)
	restarted.SetSettings(settings)
	if err := restarted.ResumeSavedSession(t.Context(), childID); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.live.ContextSnapshot(), want) || restarted.ComposerText() != "same prompt" {
		t.Fatal("restart lost exact context/prefill")
	}
	// Finish the previous UI's owned persistence command before replacing it.
	driveBeforeCommand(f, apply)
	f.ui = restarted
	// Reload after that owned command completes; this models a clean restart.
	if err := f.ui.ResumeSavedSession(t.Context(), childID); err != nil {
		t.Fatal(err)
	}
	var request llm.CompletionRequest
	f.provider.request = func(got llm.CompletionRequest) { request = got }
	settleRewindTurn(t, f, "edited continuation")
	var requestText strings.Builder
	for _, message := range request.Messages {
		requestText.WriteString(message.Content)
		requestText.WriteByte('\n')
	}
	joined := requestText.String()
	if strings.Count(joined, "same prompt") != 1 || !strings.Contains(joined, "edited continuation") || !strings.Contains(joined, rewind.FilesUnchangedNotice) {
		t.Fatalf("next request included discarded history or lost metadata: %s", joined)
	}
	completed := f.live.ContextSnapshot()
	storedChild, err := f.store.GetSessionResumeState(t.Context(), childID)
	if err != nil {
		t.Fatal(err)
	}
	head, err := rewind.DecodeResume(storedChild.Session.ContextJSON)
	if err != nil || head.SettledTurnID == "" || head.EventWatermark != storedChild.Transcript.Revision {
		t.Fatalf("child settlement identity not durably paired: %#v %v", head, err)
	}
	if err := f.ui.ResumeSavedSession(t.Context(), childID); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.live.ContextSnapshot(), completed) {
		t.Fatal("latest exact child context lost on resume")
	}
	f.provider.check = func(ctx context.Context) {
		candidates, err := f.store.ListSessionCheckpoints(ctx, childID)
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range candidates {
			record, err := f.store.GetSessionCheckpoint(ctx, childID, candidate.ID)
			if err != nil {
				t.Fatal(err)
			}
			checkpoint, err := rewind.Load(ctx, f.store, record)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := checkpoint.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.Boundary.PromptText == "after restart" {
				if snapshot.Boundary.SettledTurnID != head.SettledTurnID || snapshot.Boundary.EventWatermark != head.EventWatermark {
					t.Fatal("restart did not preserve explicit settlement identity")
				}
				return
			}
		}
		t.Fatal("missing restarted BEFORE checkpoint")
	}
	settleRewindTurn(t, f, "after restart")
	if err := f.ui.ResumeSavedSession(t.Context(), sourceID); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.live.ContextSnapshot(), sourceContext(t, source.Session.ContextJSON)) {
		t.Fatal("source context changed")
	}
}

func sourceContext(t *testing.T, data []byte) []llm.Message {
	t.Helper()
	head, err := rewind.DecodeResume(data)
	if err != nil {
		t.Fatal(err)
	}
	return head.Context
}

func TestRewindRejectsRuntimeChangeAfterPreview(t *testing.T) {
	f := newBeforeFixture(t)
	f.provider.check = func(context.Context) {}
	settleRewindTurn(t, f, "first")
	sourceID, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
	if err != nil {
		t.Fatal(err)
	}
	previewRewindPrompt(f, 1)
	f.live.SetPlanMode(true)
	f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	active, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
	if err != nil || active != sourceID {
		t.Fatal("stale runtime preview activated")
	}
	if !strings.Contains(f.ui.ToastText(), "runtime changed") {
		t.Fatal(f.ui.ToastText())
	}
}

func TestRewindBeforeFirstOfMultiplePrompts(t *testing.T) {
	f := newBeforeFixture(t)
	f.provider.check = func(context.Context) {}
	settleRewindTurn(t, f, "first prompt")
	settleRewindTurn(t, f, "second prompt")
	previewRewindPrompt(f, 2)
	_, cmd := f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	driveBeforeCommand(f, cmd)
	want := []llm.Message{{Role: llm.RoleUser, Content: rewind.FilesUnchangedNotice}}
	if !reflect.DeepEqual(f.live.ContextSnapshot(), want) || f.ui.ComposerText() != "first prompt" {
		t.Fatal("before-first selection retained later context or lost prefill")
	}
}
