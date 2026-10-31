package tui_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestResumeSavedTargetFailureRetainsCurrentSelection(t *testing.T) {
	settings := newTestSettings(t)
	oldSelection := prefs.ModelSelection{Provider: "llamacpp", Model: "old-model"}
	requireSetSelection(t, settings, prefs.ScopeWorkspace, oldSelection)
	ui, live := newTargetUI(t, settings, oldSelection)
	before := live.RunTarget()
	beforeSession := ui.SessionIdentity()

	const sessionID = "resume-invalid-target"
	if err := settings.Store.SaveSessionDraft(t.Context(), db.SessionRecord{
		ID:          sessionID,
		Workspace:   settings.WorkspaceRoot(),
		Provider:    "not-a-provider",
		Model:       "saved-model",
		PendingJSON: []byte(`{"text":"unfinished prompt"}`),
	}); err != nil {
		t.Fatal(err)
	}

	cmd, requested, err := ui.ResumeSavedSessionTarget(t.Context(), sessionID)
	if err == nil || !strings.Contains(err.Error(), "provider switch failed") {
		t.Fatalf("resume error = %v, want provider switch failure", err)
	}
	if !requested || cmd != nil {
		t.Fatal("rejected saved target returned a command")
	}

	requireSelection(t, settings, prefs.ScopeWorkspace, oldSelection)
	got := live.RunTarget()
	if got.Provider != before.Provider || got.Spec != before.Spec || got.Window != before.Window {
		t.Fatalf("live target changed after resumed target failed: got=%+v before=%+v", got, before)
	}
	if got := ui.SessionIdentity(); got != beforeSession {
		t.Fatalf("rejected session published identity %q", got)
	}
}
