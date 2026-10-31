package tui_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/ai/llm/claudecode"
	"github.com/zarldev/zarlmono/zkit/db"
)

type sessionFixtureKeys struct{}

func (sessionFixtureKeys) GetKey(context.Context, string) (string, error) { return "fixture-key", nil }

func TestSavedTargetResumeBuildsBeforePublishingProviderSpecificContext(t *testing.T) {
	for _, rejectWrite := range []bool{false, true} {
		name := "success"
		if rejectWrite {
			name = "persistence rollback"
		}
		t.Run(name, func(t *testing.T) {
			var request []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				request, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: "+`{"candidates":[{"content":{"role":"model","parts":[{"text":"continued"}]},"finishReason":"STOP"}]}`+"\n\n")
			}))
			t.Cleanup(server.Close)
			f := newBeforeFixture(t)
			f.provider.check = func(context.Context) {}
			settleRewindTurn(t, f, "current transcript")
			sourceID := f.ui.SessionIdentity()
			settings := engine.NewSettings(f.store, nil, nil, f.ws.Root())
			settings.Registry = backends.NewRegistry(backends.WithProviderDefinitions([]backends.ProviderDefinition{{Name: "gemini", DisplayName: "Gemini fixture", AdapterType: backends.AdapterTypes.GOOGLECOMPATIBLE, BaseURL: server.URL, DefaultModel: "fixture", ContextWindow: 32768, Enabled: true, Builtin: true}}), backends.WithSettingsService(sessionFixtureKeys{}))
			f.ui.SetSettings(settings)
			current := claudecode.NewProvider(claudecode.StaticTokenSource{T: claudecode.Token{Access: "fixture-token"}})
			currentSpec := engine.ProviderSpec{Name: "claude-code", Model: "sonnet"}
			f.ui.RepointProvider(current, currentSpec, 32768, nil)
			oldSelection := prefs.ModelSelection{Provider: "claude-code", Model: "sonnet"}
			requireSetSelection(t, settings, prefs.ScopeWorkspace, oldSelection)
			before := f.live.RunTarget()
			beforeContext := f.live.ContextSnapshot()
			beforeThread := f.ui.CanonicalThread()
			saved, err := f.store.GetSession(t.Context(), sourceID)
			if err != nil {
				t.Fatal(err)
			}
			saved.ID = "saved-gemini"
			saved.Provider = "gemini"
			saved.Model = "fixture"
			messages := []llm.Message{{Role: llm.RoleUser, Content: "current transcript", Parts: []llm.ContentPart{{Type: llm.ContentTypeText, Text: "current transcript"}, {Type: llm.ContentTypeImage, Image: &llm.ImageData{DataURI: "data:image/png;base64,YQ==", MIMEType: "image/png"}}}}}
			saved.ContextJSON, err = json.Marshal(messages)
			if err != nil {
				t.Fatal(err)
			}
			thread, err := f.store.GetSessionTranscript(t.Context(), sourceID)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.store.CommitCompletedTurn(t.Context(), saved, db.TranscriptUpdate{SessionID: saved.ID, Workspace: f.ws.Root(), Revision: thread.Revision, Entries: thread.Entries}); err != nil {
				t.Fatal(err)
			}
			// Creating the fixture uses an active-transcript transaction; reselect
			// the source before testing the later resume transaction.
			if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, prefs.KeyActiveSession, sourceID); err != nil {
				t.Fatal(err)
			}
			if rejectWrite {
				if _, err := f.store.DB().ExecContext(t.Context(), `CREATE TRIGGER reject_session_selection BEFORE UPDATE ON settings WHEN NEW.key = 'active_session' BEGIN SELECT RAISE(ABORT, 'injected session selection rejection'); END`); err != nil {
					t.Fatal(err)
				}
			}
			_, requested, err := f.ui.ResumeSavedSessionTarget(t.Context(), saved.ID)
			if !requested {
				t.Fatal("saved target was not requested")
			}
			if rejectWrite {
				if err == nil {
					t.Fatal("persistence rejection was ignored")
				}
				active, readErr := f.store.GetSettingExact(t.Context(), f.ws.Root(), prefs.KeyActiveSession)
				target := f.live.RunTarget()
				if readErr != nil || active != sourceID || target.Provider != before.Provider || target.Spec != before.Spec || !reflect.DeepEqual(f.live.ContextSnapshot(), beforeContext) || !reflect.DeepEqual(f.ui.CanonicalThread(), beforeThread) || f.ui.SessionIdentity() != sourceID {
					t.Fatal("failed selection changed active state")
				}
				requireSelection(t, settings, prefs.ScopeWorkspace, oldSelection)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			target := f.live.RunTarget()
			if target.Spec.Name != "gemini" || target.Spec.Model != "fixture" || f.ui.ActiveProviderSpec() != target.Spec || !reflect.DeepEqual(f.live.ContextSnapshot(), messages) || f.ui.SessionIdentity() != saved.ID {
				t.Fatal("prepared target and context were not published together")
			}
			requireSelection(t, settings, prefs.ScopeWorkspace, prefs.ModelSelection{Provider: "gemini", Model: "fixture"})
			active, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), prefs.KeyActiveSession)
			if err != nil || active != saved.ID {
				t.Fatal("saved session preference not selected")
			}
			if err := f.live.RunTurn(t.Context(), "continue"); err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(request), "current transcript") != 1 || !strings.Contains(string(request), `"data":"YQ=="`) {
				t.Fatal("continued saved target lost image or duplicated prompt")
			}
		})
	}
}
