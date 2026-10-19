package tui_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestRequestControlProjectsOnceWithoutRenderingNotice(t *testing.T) {
	control := runner.ReplayControl{
		Version:       1,
		TurnID:        "original-turn",
		ID:            "control-1",
		PolicyLabel:   "build",
		SystemDigest:  strings.Repeat("a", 64),
		SurfaceDigest: strings.Repeat("b", 64),
	}
	var model tea.Model = tui.New()
	model, _ = model.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	model, _ = model.Update(teasink.ConversationStartedMsg{TaskID: "turn-1", Prompt: "project control"})
	message := teasink.RequestControlRecordedMsg{TaskID: "delivery-turn", Control: control}
	model, _ = model.Update(message)
	model, _ = model.Update(message)

	ui := model.(*tui.UI)
	var count int
	for _, entry := range ui.CanonicalThread().Entries() {
		if entry.Kind == transcript.EntryKinds.ENTRYCONTROLCHANGE {
			count++
			if entry.TurnID != "original-turn" {
				t.Fatalf("control turn = %q, want original-turn", entry.TurnID)
			}
		}
	}
	if count != 1 {
		t.Fatalf("control entries = %d, want 1", count)
	}
	assertControlHidden(t, ui)
}

func TestRequestControlRecoveryWithoutAssistantPersistsAndReloadsIdempotently(t *testing.T) {
	control := recoveryControl("control-crash", "turn-original", "build", 'b')
	thread := reduceRecoveryThread(t, transcript.UserSubmitted{Text: "request"})
	store, workspace, sessionID := recoverySession(t, thread, []runner.ReplayControl{control})

	first := resumeRecoverySession(t, store, workspace, sessionID)
	assertControlHidden(t, first)
	if view := first.View().Content; !strings.Contains(view, "request") {
		t.Fatalf("restored chat missing user message:\n%s", view)
	}
	entries := first.CanonicalThread().Entries()
	if len(entries) != 2 || entries[1].ID != control.ID || entries[1].TurnID != control.TurnID {
		t.Fatalf("recovered entries = %+v", entries)
	}
	stored, err := store.GetSessionTranscript(t.Context(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Revision != thread.Revision()+1 {
		t.Fatalf("stored revision = %d, want %d", stored.Revision, thread.Revision()+1)
	}

	second := resumeRecoverySession(t, store, workspace, sessionID)
	assertControlHidden(t, second)
	if got := second.CanonicalThread().Entries(); len(got) != 2 || got[1].TurnID != control.TurnID {
		t.Fatalf("reloaded entries = %+v", got)
	}
	reloaded, err := store.GetSessionTranscript(t.Context(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Revision != stored.Revision {
		t.Fatalf("idempotent reload advanced revision from %d to %d", stored.Revision, reloaded.Revision)
	}
}

func TestRequestControlRecoveryPreservesExistingSameTurnAttribution(t *testing.T) {
	first := recoveryControl("control-one", "same-turn", "build", 'b')
	second := recoveryControl("control-two", "same-turn", "plan", 'c')
	thread := reduceRecoveryThread(t,
		transcript.TurnStarted{TurnID: "other-turn"},
		transcript.TurnFinished{TurnID: "other-turn"},
		transcript.ControlRecorded{TurnID: "same-turn", Control: first},
		transcript.ControlRecorded{TurnID: "same-turn", Control: second},
	)
	store, workspace, sessionID := recoverySession(t, thread, []runner.ReplayControl{first, second})

	ui := resumeRecoverySession(t, store, workspace, sessionID)
	var controls []transcript.Entry
	for _, entry := range ui.CanonicalThread().Entries() {
		if entry.Kind == transcript.EntryKinds.ENTRYCONTROLCHANGE {
			controls = append(controls, entry)
		}
	}
	if len(controls) != 2 || controls[0].TurnID != "same-turn" || controls[1].TurnID != "same-turn" {
		t.Fatalf("control attribution = %+v", controls)
	}
	stored, err := store.GetSessionTranscript(t.Context(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Revision != thread.Revision() {
		t.Fatalf("duplicate recovery advanced revision from %d to %d", thread.Revision(), stored.Revision)
	}
}

func TestRequestControlRecoveryRejectsConflictWithoutOverwritingTranscript(t *testing.T) {
	storedControl := recoveryControl("control-conflict", "turn-one", "build", 'b')
	thread := reduceRecoveryThread(t,
		transcript.ControlRecorded{TurnID: "turn-one", Control: storedControl},
		transcript.TurnStarted{TurnID: "interrupted-turn"},
	)
	conflict := storedControl.Clone()
	conflict.PolicyLabel = "plan"
	store, workspace, sessionID := recoverySession(t, thread, []runner.ReplayControl{conflict})
	before, err := store.GetSessionTranscript(t.Context(), sessionID)
	if err != nil {
		t.Fatal(err)
	}

	ui := tui.New()
	ui.SetSettings(engine.NewSettings(store, nil, nil, workspace))
	if err := ui.ResumeSavedSession(t.Context(), sessionID); err == nil {
		t.Fatal("conflicting control recovery succeeded")
	}
	after, err := store.GetSessionTranscript(t.Context(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision || len(after.Entries) != len(before.Entries) {
		t.Fatalf("conflict overwrote transcript: before=%+v after=%+v", before, after)
	}
}

func TestRequestControlRecoveryUsesExplicitLegacyStandaloneAttribution(t *testing.T) {
	control := recoveryControl("legacy-control", "", "build", 'd')
	thread := reduceRecoveryThread(t, transcript.UserSubmitted{Text: "request"})
	store, workspace, sessionID := recoverySession(t, thread, []runner.ReplayControl{control})

	ui := resumeRecoverySession(t, store, workspace, sessionID)
	entries := ui.CanonicalThread().Entries()
	if got, want := entries[len(entries)-1].TurnID, "recovered-control:"+control.ID; got != want {
		t.Fatalf("legacy recovered turn = %q, want %q", got, want)
	}
}

func assertControlHidden(t *testing.T, ui *tui.UI) {
	t.Helper()
	ui.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	view := ui.View().Content
	for _, unwanted := range []string{"control ·", "aaaaaaaaaaaa", "bbbbbbbbbbbb"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("chat contains control diagnostic %q:\n%s", unwanted, view)
		}
	}
}

func recoveryControl(id, turnID, policy string, digestByte byte) runner.ReplayControl {
	return runner.ReplayControl{
		Version: 1, ID: id, TurnID: turnID, PolicyLabel: policy,
		SystemDigest: strings.Repeat("a", 64), SurfaceDigest: strings.Repeat(string(digestByte), 64),
	}
}

func reduceRecoveryThread(t *testing.T, events ...any) transcript.Thread {
	t.Helper()
	reducer := transcript.NewReducer()
	for _, event := range events {
		if _, err := reducer.Apply(event); err != nil {
			t.Fatalf("reduce fixture event %T: %v", event, err)
		}
	}
	return reducer.Thread()
}

func recoverySession(t *testing.T, thread transcript.Thread, controls []runner.ReplayControl) (*db.Store, string, string) {
	t.Helper()
	workspace := t.TempDir()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "recovery.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	const sessionID = "control-recovery"
	if err := store.SaveActiveSession(t.Context(), db.SessionRecord{
		ID: sessionID, Workspace: workspace, ContextJSON: []byte(`[]`), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	ref, err := store.CaptureSessionHistory(t.Context(), sessionID, nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSessionCheckpoint(t.Context(), db.SessionCheckpoint{
		SessionID: sessionID, ID: "initial", SourceSessionID: sessionID,
		Workspace: workspace, BoundaryID: "prompt", FormatVersion: 2,
		Payload: []byte(`{}`), History: ref,
	}); err != nil {
		t.Fatal(err)
	}
	if !thread.IsEmpty() {
		records, err := thread.RecordsSince(0)
		if err != nil {
			t.Fatal(err)
		}
		entries := make([]db.TranscriptEntry, len(records))
		for i, record := range records {
			entries[i] = db.TranscriptEntry{Sequence: record.Sequence, EntryID: record.ID, ParentID: record.ParentID,
				TurnID: record.TurnID, Kind: record.Kind, PayloadJSON: record.Payload, Revision: record.Revision}
		}
		if err := store.UpdateActiveTranscript(t.Context(), db.TranscriptUpdate{
			SessionID: sessionID, Workspace: workspace, CreatedAt: time.Now(), Revision: thread.Revision(), Entries: entries,
		}); err != nil {
			t.Fatal(err)
		}
	}
	saveRecoveryControls(t, store, sessionID, controls)
	return store, workspace, sessionID
}

func resumeRecoverySession(t *testing.T, store *db.Store, workspace, sessionID string) *tui.UI {
	t.Helper()
	ui := tui.New()
	ui.SetSettings(engine.NewSettings(store, nil, nil, workspace))
	if err := ui.ResumeSavedSession(t.Context(), sessionID); err != nil {
		t.Fatal(err)
	}
	return ui
}

func saveRecoveryControls(t *testing.T, store *db.Store, sessionID string, controls []runner.ReplayControl) {
	t.Helper()
	messages := make([][]byte, len(controls))
	for i, control := range controls {
		var err error
		messages[i], err = runner.MarshalReplayMessage(runner.ReplayMessage{Kind: runner.ReplayOccurrenceKinds.CONTROL, Control: control})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveSessionHistory(t.Context(), sessionID, []db.SessionHistoryBatch{{ID: "controls", Messages: messages}}); err != nil {
		t.Fatal(err)
	}
}
