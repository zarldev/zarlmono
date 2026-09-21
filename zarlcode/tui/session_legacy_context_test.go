package tui_test

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestResumeLegacyContextPreservesValidationAndStoredBytes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, provider, context string
		invalid                 bool
	}{
		{name: "missing provider", context: `[{"role":"user","content":" historical prompt "}]`},
		{name: "non-exact provider", provider: "llamacpp", context: `[{"role":"user","content":" historical prompt "}]`},
		{name: "orphan result", context: `[{"role":"tool","tool_call_id":"missing","content":"result"}]`, invalid: true},
		{name: "dangling call", provider: "llamacpp", context: `[{"role":"assistant","tool_calls":[{"id":"call","type":"function","function":{"name":"read","arguments":"{}"}}]}]`, invalid: true},
		{name: "unknown role", context: `[{"role":"unknown","content":"prompt"}]`, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			workspace, err := code.NewWorkspace(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "session.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			const sessionID = "legacy-session"
			if err := store.SaveSession(t.Context(), db.SessionRecord{
				ID: sessionID, Workspace: workspace.Root(), Provider: tc.provider,
				ContextJSON: []byte(tc.context), MessageCount: 1,
			}); err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateActiveTranscript(t.Context(), db.TranscriptUpdate{
				SessionID: sessionID, Workspace: workspace.Root(), Revision: 1,
				Entries: []db.TranscriptEntry{{Sequence: 1, EntryID: "prompt", Kind: "user_message", PayloadJSON: []byte(`{"text":"canonical prompt"}`), Revision: 1}},
			}); err != nil {
				t.Fatal(err)
			}
			live := engine.NewLiveRunner(nil, workspace, "test-model")
			ui := tui.New()
			ui.SetLiveRunner(live)
			ui.SetSettings(engine.NewSettings(store, nil, nil, workspace.Root()))
			err = ui.ResumeSavedSession(t.Context(), sessionID)
			if tc.invalid {
				if !errors.Is(err, rewind.ErrInvalid) {
					t.Fatalf("resume malformed legacy context = %v", err)
				}
				if ui.SessionIdentity() != "" || len(live.ContextSnapshot()) != 0 {
					t.Fatal("rejected legacy context changed the active session")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if got := live.ContextSnapshot(); len(got) != 1 || got[0].Content != " historical prompt " {
					t.Fatalf("restored context = %#v", got)
				}
			}
			saved, err := store.GetSessionResumeState(t.Context(), sessionID)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(saved.Session.ContextJSON, []byte(tc.context)) || saved.Transcript.Revision != 1 {
				t.Fatal("legacy resume rewrote stored context or transcript")
			}
		})
	}
}
