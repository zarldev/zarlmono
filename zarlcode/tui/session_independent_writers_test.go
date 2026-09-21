package tui_test

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestIndependentConversationDoesNotPauseTurnSettlement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newBeforeFixture(t)
		f.ui.SetStartupReady(true)
		f.provider.check = func(context.Context) {}
		settleRewindTurn(t, f, "first prompt")
		id, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
		if err != nil {
			t.Fatal(err)
		}
		other := db.SessionRecord{ID: "other-conversation", Workspace: f.ws.Root(), ContextJSON: []byte(`[]`)}
		selectOther := func(ctx context.Context) {
			t.Helper()
			if err := f.store.SaveActiveSession(ctx, other); err != nil {
				t.Fatal(err)
			}
		}
		// Another conversation becomes active both before dispatch and while the
		// model is running. Neither may invalidate this conversation's save.
		selectOther(t.Context())
		calls := 0
		f.provider.check = func(ctx context.Context) {
			calls++
			selectOther(ctx)
		}
		for _, prompt := range []string{"second prompt", "third prompt"} {
			settleRewindTurn(t, f, prompt)
			if strings.Contains(f.ui.ToastText(), "changed elsewhere") {
				t.Fatal("independent conversation paused local saves")
			}
			if err := f.ui.SaveSession(t.Context()); err != nil {
				t.Fatalf("completed turn not saveable: %v", err)
			}
			saved, err := f.store.GetSessionResumeState(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			resume, err := rewind.DecodeResume(saved.Session.ContextJSON)
			if err != nil || !strings.Contains(string(saved.Session.ContextJSON), prompt) {
				t.Fatalf("completed context not durable: %v", err)
			}
			if resume.Revision != saved.Transcript.Revision {
				t.Fatal("completed context and transcript diverged")
			}
		}
		if calls != 2 {
			t.Fatalf("subsequent turns blocked: %d calls, want 2", calls)
		}
	})
}
