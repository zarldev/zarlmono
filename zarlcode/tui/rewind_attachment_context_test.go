package tui_test

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestAttachmentContextSurvivesSettlementBranchAndRestart(t *testing.T) {
	for _, provider := range []string{"openai", "openai-codex"} {
		t.Run(provider, func(t *testing.T) {
			f := newBeforeFixture(t)
			settings := engine.NewSettings(f.store, nil, nil, f.ws.Root())
			settings.Registry = nil // fixture supplies its provider directly
			f.ui.SetSettings(settings)
			f.live.SetProviderSpec(nativeSettlementProvider{name: provider, chunk: llm.CompletionChunk{Content: "answer"}},
				engine.ProviderSpec{Name: provider, Model: "saved-model"})
			var picture bytes.Buffer
			if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			for name, data := range map[string][]byte{"notes.txt": []byte("  attachment bytes\n"), "image.png": picture.Bytes()} {
				path := filepath.Join(root, name)
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
				attach := f.ui.AttachFile
				if name == "image.png" {
					attach = f.ui.AttachImage
				}
				if err := attach(path); err != nil {
					t.Fatal(err)
				}
			}
			settleRewindTurn(t, f, "inspect attachments")
			historical := f.live.ContextSnapshot()
			if len(historical) == 0 || historical[0].Content != "inspect attachments" || len(historical[0].Parts) != 3 {
				t.Fatalf("fixture context = %#v; toast = %s", historical, f.ui.ToastText())
			}
			stored, err := f.store.GetSessionResumeState(t.Context(), f.ui.SessionIdentity())
			if err != nil {
				t.Fatal(err)
			}
			head, err := rewind.DecodeResume(stored.Session.ContextJSON)
			if err != nil || !reflect.DeepEqual(head.Context, historical) {
				t.Fatalf("attachment settlement lost context: %v; %s", err, f.ui.ToastText())
			}
			settleRewindTurn(t, f, "second")
			sourceID := f.ui.SessionIdentity()
			previewRewindPrompt(f, 1)
			_, apply := f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			driveBeforeCommand(f, apply)
			childID := f.ui.SessionIdentity()
			if childID == sourceID {
				t.Fatalf("attachment branch not activated: %s", f.ui.ToastText())
			}
			want := append(llm.CloneMessages(historical), llm.Message{Role: llm.RoleUser, Content: rewind.FilesUnchangedNotice})
			if !reflect.DeepEqual(f.live.ContextSnapshot(), want) {
				t.Fatal("activation lost attachments")
			}
			restarted := tui.New()
			restarted.SetLiveRunner(f.live)
			restarted.SetLiveEventSink(f.sink)
			restarted.SetSettings(settings)
			if err := restarted.ResumeSavedSession(t.Context(), childID); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(f.live.ContextSnapshot(), want) || restarted.ComposerText() != "second" {
				t.Fatal("restart lost attachment context or prompt prefill")
			}
		})
	}
}
