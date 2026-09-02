package tui_test

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestResumeUsesDurableTranscriptInsteadOfCompactedContextHistory(t *testing.T) {
	workspaceRoot := t.TempDir()
	workspace, err := code.NewWorkspace(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	const sessionID = "durable-resume"
	record := db.SessionRecord{
		ID:           sessionID,
		Workspace:    workspaceRoot,
		ContextJSON:  []byte(`[{"role":"user","content":"compacted context only"}]`),
		MessageCount: 1,
	}
	if err := store.SaveSession(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateActiveTranscript(t.Context(), db.TranscriptUpdate{
		SessionID: sessionID, Workspace: workspaceRoot, Revision: 2, Entries: []db.TranscriptEntry{
			{Sequence: 1, EntryID: "e1", Kind: "user_message", PayloadJSON: []byte(`{"text":"original first turn"}`), Revision: 1},
			{Sequence: 2, EntryID: "e2", TurnID: "turn-1", Kind: "assistant_message", PayloadJSON: []byte(`{"text":"original answer","complete":true}`), Revision: 2},
		},
	}); err != nil {
		t.Fatal(err)
	}

	live := engine.NewLiveRunner(nil, workspace, "test-model")
	ui := tui.New()
	ui.SetLiveRunner(live)
	ui.SetSettings(engine.NewSettings(store, nil, nil, workspaceRoot))
	if err := ui.ResumeSavedSession(t.Context(), sessionID); err != nil {
		t.Fatal(err)
	}
	var model tea.Model = ui
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	out := ansi.Strip(model.View().Content)
	if !strings.Contains(out, "original first turn") || !strings.Contains(out, "original answer") {
		t.Fatalf("resume did not restore durable transcript:\n%s", out)
	}
	if strings.Contains(out, "compacted context only") {
		t.Fatalf("resume rendered compacted model history instead of transcript:\n%s", out)
	}
	if history := live.ContextSnapshot(); len(history) != 1 || history[0].Content != "compacted context only" {
		t.Fatalf("runner history = %#v, want compacted context", history)
	}
}

func TestResumeDraftOnlySessionWithoutTranscript(t *testing.T) {
	workspaceRoot := t.TempDir()
	workspace, err := code.NewWorkspace(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveSessionDraft(t.Context(), db.SessionRecord{
		ID: "draft-only", Workspace: workspaceRoot, PendingJSON: []byte(`{"text":"unfinished prompt"}`),
	}); err != nil {
		t.Fatal(err)
	}

	ui := tui.New()
	ui.SetLiveRunner(engine.NewLiveRunner(nil, workspace, "test-model"))
	ui.SetSettings(engine.NewSettings(store, nil, nil, workspaceRoot))
	if err := ui.ResumeSavedSession(t.Context(), "draft-only"); err != nil {
		t.Fatal(err)
	}
	if got := ui.ComposerText(); got != "unfinished prompt" {
		t.Fatalf("composer text = %q", got)
	}
	if got := ui.CanonicalThread(); !got.IsEmpty() {
		t.Fatalf("draft-only transcript = %#v, want empty", got.Entries())
	}
}

func TestResumeRejectsSessionWithoutCanonicalTranscript(t *testing.T) {
	workspaceRoot := t.TempDir()
	workspace, err := code.NewWorkspace(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveSession(t.Context(), db.SessionRecord{ID: "no-transcript", Workspace: workspaceRoot, ContextJSON: []byte(`[{"role":"user","content":"context is not a transcript"}]`)}); err != nil {
		t.Fatal(err)
	}
	ui := tui.New()
	ui.SetLiveRunner(engine.NewLiveRunner(nil, workspace, "test-model"))
	ui.SetSettings(engine.NewSettings(store, nil, nil, workspaceRoot))
	err = ui.ResumeSavedSession(t.Context(), "no-transcript")
	if err == nil || !strings.Contains(err.Error(), "session transcript not found") {
		t.Fatalf("resume error = %v, want missing transcript", err)
	}
}

func TestMarkdownSessionExportUsesVisibleTranscriptInsteadOfCompactedHistory(t *testing.T) {
	target := filepath.Join(t.TempDir(), "durable-export.md")
	workspace, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	live := engine.NewLiveRunner(nil, workspace, "test-model")
	live.RestoreContext([]llm.Message{{Role: llm.RoleUser, Content: "compacted context only"}})
	ui := tui.New()
	ui.SetLiveRunner(live)
	ui.AddTranscriptMessages([]llm.Message{
		{Role: llm.RoleUser, Content: "original first turn"},
		{Role: llm.RoleAssistant, Content: "original answer"},
	})
	var model tea.Model = ui
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model, _ = model.Update(tea.PasteMsg{Content: "/export " + target})
	_, exportCmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if exportCmd == nil {
		t.Fatal("export returned no command")
	}
	exportCmd()
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "original first turn") || !strings.Contains(string(body), "original answer") {
		t.Fatalf("export missing durable transcript:\n%s", body)
	}
	if strings.Contains(string(body), "compacted context only") {
		t.Fatalf("export used compacted model history:\n%s", body)
	}
}

func TestSavedTranscriptRoundTripsLiveConversationIndependentlyOfContext(t *testing.T) {
	workspaceRoot := t.TempDir()
	workspace, err := code.NewWorkspace(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	live := engine.NewLiveRunner(nil, workspace, "test-model")
	live.RestoreContext([]llm.Message{{Role: llm.RoleUser, Content: "compacted context only"}})
	ui := tui.New()
	ui.SetLiveRunner(live)
	ui.SetSettings(engine.NewSettings(store, nil, nil, workspaceRoot))
	ui.SetSessionIdentity("round-trip", "Round trip", false, time.Now())
	var model tea.Model = ui
	model, _ = model.Update(teasink.ConversationStartedMsg{TaskID: "turn", Prompt: "original first turn"})
	model, _ = model.Update(teasink.ContentMsg{TaskID: "turn", Delta: "original answer"})
	model, _ = model.Update(teasink.ConversationEndedMsg{TaskID: "turn", Reason: runner.TerminalCompleted})
	ui = model.(*tui.UI)
	if err := ui.SaveSession(t.Context()); err != nil {
		t.Fatal(err)
	}

	saved, err := store.GetSession(t.Context(), "round-trip")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved.ContextJSON), "compacted context only") {
		t.Fatalf("context history was not saved independently: %s", saved.ContextJSON)
	}
	transcript, err := store.GetSessionTranscript(t.Context(), "round-trip")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tui.TranscriptText(transcript), "original first turn") || !strings.Contains(tui.TranscriptText(transcript), "original answer") {
		t.Fatalf("durable transcript missing live conversation: %s", tui.TranscriptText(transcript))
	}
	for _, forbidden := range []string{"expanded", "closed", "nested", "has_activity", "wait_duration"} {
		if strings.Contains(strings.ToLower(tui.TranscriptText(transcript)), forbidden) {
			t.Fatalf("durable transcript contains renderer state %q: %s", forbidden, tui.TranscriptText(transcript))
		}
	}

	resumed := tui.New()
	resumed.SetLiveRunner(engine.NewLiveRunner(nil, workspace, "test-model"))
	resumed.SetSettings(engine.NewSettings(store, nil, nil, workspaceRoot))
	if err := resumed.ResumeSavedSession(t.Context(), "round-trip"); err != nil {
		t.Fatal(err)
	}
	var resumedModel tea.Model = resumed
	resumedModel, _ = resumedModel.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	out := ansi.Strip(resumedModel.View().Content)
	if !strings.Contains(out, "original first turn") || !strings.Contains(out, "original answer") {
		t.Fatalf("resumed transcript lost live conversation:\n%s", out)
	}
	if strings.Contains(out, "compacted context only") {
		t.Fatalf("resumed UI used compacted context:\n%s", out)
	}
}

func TestMarkdownSessionExportCompletesWithoutBlockingTheUI(t *testing.T) {
	target := filepath.Join(t.TempDir(), "session.md")
	workspace, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	live := engine.NewLiveRunner(nil, workspace, "test-model")
	live.RestoreContext([]llm.Message{{Role: llm.RoleUser, Content: "export me"}})
	ui := tui.New()
	ui.SetLiveRunner(live)
	ui.AddTranscriptMessages([]llm.Message{{Role: llm.RoleUser, Content: "export me"}})
	var model tea.Model = ui
	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	model, _ = model.Update(tea.PasteMsg{Content: "/export " + target})
	model, exportCmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if exportCmd == nil {
		t.Fatal("export submission returned no command")
	}

	model, toastCmd := model.Update(exportCmd())
	if toastCmd == nil {
		t.Fatal("successful export returned no toast expiry command")
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	if !strings.Contains(string(body), "## Conversation") || !strings.Contains(string(body), "export me") {
		t.Fatalf("export missing conversation Markdown:\n%s", body)
	}
	if out := ansi.Strip(model.View().Content); !strings.Contains(out, "exported") {
		t.Fatalf("UI missing export confirmation:\n%s", out)
	}

	// Bubble Tea can dispatch the same command closure more than once. The first
	// invocation owns the timer; a duplicate must return instead of waiting on the
	// already-drained timer and wedging program shutdown.
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		toastCmd()
	}()
	time.Sleep(25 * time.Millisecond)

	duplicateDone := make(chan struct{})
	go func() {
		defer close(duplicateDone)
		toastCmd()
	}()
	select {
	case <-duplicateDone:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("duplicate export toast command blocked")
	}
	select {
	case <-firstDone:
	case <-time.After(5 * time.Second):
		t.Fatal("export toast command did not terminate")
	}
}

func TestMarkdownSessionExportRejectsExistingExplicitPath(t *testing.T) {
	target := filepath.Join(t.TempDir(), "session.md")
	const original = "keep this file"
	if err := os.WriteFile(target, []byte(original), 0o600); err != nil {
		t.Fatalf("seed export target: %v", err)
	}
	workspace, err := code.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	live := engine.NewLiveRunner(nil, workspace, "test-model")
	live.RestoreContext([]llm.Message{{Role: llm.RoleUser, Content: "export me"}})
	ui := tui.New()
	ui.SetLiveRunner(live)
	ui.AddTranscriptMessages([]llm.Message{{Role: llm.RoleUser, Content: "export me"}})
	var model tea.Model = ui
	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	model, _ = model.Update(tea.PasteMsg{Content: "/export " + target})
	model, exportCmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if exportCmd == nil {
		t.Fatal("export submission returned no command")
	}

	model, _ = model.Update(exportCmd())
	out := ansi.Strip(model.View().Content)
	if !strings.Contains(out, "export: create export") || !strings.Contains(out, target) {
		t.Fatalf("existing explicit path did not produce a collision error:\n%s", out)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read original target: %v", err)
	}
	if string(body) != original {
		t.Fatalf("explicit export overwrote existing target: %q", body)
	}
	if matches, err := filepath.Glob(filepath.Join(filepath.Dir(target), "session-*.md")); err != nil {
		t.Fatalf("glob alternate exports: %v", err)
	} else if len(matches) != 0 {
		t.Fatalf("explicit export silently wrote alternate path: %v", matches)
	}
}

func TestResumeRejectsCorruptTranscriptWithoutReplacingCurrentUI(t *testing.T) {
	workspaceRoot := t.TempDir()
	workspace, err := code.NewWorkspace(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(t.TempDir(), "sessions.db")
	store, err := db.Open(t.Context(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpdateActiveTranscript(t.Context(), db.TranscriptUpdate{SessionID: "corrupt", Workspace: workspaceRoot, Revision: 1, Entries: []db.TranscriptEntry{{Sequence: 1, EntryID: "e1", Kind: "user_message", PayloadJSON: []byte(`{"text":"saved"}`), Revision: 1}}}); err != nil {
		t.Fatal(err)
	}
	tamperDB, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tamperDB.Close() })
	if _, err := tamperDB.ExecContext(t.Context(), `UPDATE session_transcript_entries SET payload_json = ? WHERE session_id = ?`, `{"text":"tampered"}`, "corrupt"); err != nil {
		t.Fatal(err)
	}
	ui := tui.New()
	ui.SetLiveRunner(engine.NewLiveRunner(nil, workspace, "test-model"))
	ui.SetSettings(engine.NewSettings(store, nil, nil, workspaceRoot))
	ui.AddTranscriptMessages([]llm.Message{{Role: llm.RoleUser, Content: "current thread"}})
	err = ui.ResumeSavedSession(t.Context(), "corrupt")
	if !errors.Is(err, db.ErrTranscriptCorrupt) {
		t.Fatalf("resume error = %v, want ErrTranscriptCorrupt", err)
	}
	var model tea.Model = ui
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	out := ansi.Strip(model.View().Content)
	if !strings.Contains(out, "current thread") || strings.Contains(out, "tampered") {
		t.Fatalf("resume mutated UI after corruption:\n%s", out)
	}
}

func TestResumeRecoversInterruptedTurnAndPersistsItOnce(t *testing.T) {
	workspaceRoot := t.TempDir()
	workspace, err := code.NewWorkspace(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	builder := transcript.NewBuilder()
	builder.AddUser("interrupted prompt")
	builder.StartTurn("turn", "")
	builder.AppendReasoning("turn", "", "partial thought")
	builder.AppendAssistant("turn", "", "partial answer")
	builder.StartTool("turn", "", "tool", "", "read", "main.go", 0)
	thread := builder.Thread()
	if err := store.UpdateActiveTranscript(t.Context(), transcriptDBUpdate(t, "interrupted", workspaceRoot, 0, thread)); err != nil {
		t.Fatal(err)
	}
	ui := tui.New()
	ui.SetLiveRunner(engine.NewLiveRunner(nil, workspace, "test-model"))
	ui.SetSettings(engine.NewSettings(store, nil, nil, workspaceRoot))
	if err := ui.ResumeSavedSession(t.Context(), "interrupted"); err != nil {
		t.Fatal(err)
	}
	first, err := store.GetSessionTranscript(t.Context(), "interrupted")
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision <= thread.Revision() {
		t.Fatalf("recovery revision = %d, want > %d", first.Revision, thread.Revision())
	}
	var model tea.Model = ui
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	out := ansi.Strip(model.View().Content)
	if !strings.Contains(out, "partial answer") || !strings.Contains(out, "interrupted") || strings.Contains(out, "read running") {
		t.Fatalf("recovered transcript did not render interruption:\n%s", out)
	}
	secondUI := tui.New()
	secondUI.SetLiveRunner(engine.NewLiveRunner(nil, workspace, "test-model"))
	secondUI.SetSettings(engine.NewSettings(store, nil, nil, workspaceRoot))
	if err := secondUI.ResumeSavedSession(t.Context(), "interrupted"); err != nil {
		t.Fatal(err)
	}
	second, err := store.GetSessionTranscript(t.Context(), "interrupted")
	if err != nil {
		t.Fatal(err)
	}
	if second.Revision != first.Revision {
		t.Fatalf("second resume advanced recovery revision: %d -> %d", first.Revision, second.Revision)
	}
}

func transcriptDBUpdate(t *testing.T, sessionID, workspace string, expected uint64, thread transcript.Thread) db.TranscriptUpdate {
	t.Helper()
	records, err := thread.RecordsSince(expected)
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]db.TranscriptEntry, len(records))
	for i, record := range records {
		entries[i] = db.TranscriptEntry{Sequence: record.Sequence, EntryID: record.ID, ParentID: record.ParentID, TurnID: record.TurnID, Kind: record.Kind, PayloadJSON: record.Payload, Revision: record.Revision}
	}
	return db.TranscriptUpdate{SessionID: sessionID, Workspace: workspace, ExpectedRevision: expected, Revision: thread.Revision(), Entries: entries}
}

func TestRecoveredSessionAcceptsAndPersistsNextTurn(t *testing.T) {
	workspaceRoot := t.TempDir()
	workspace, err := code.NewWorkspace(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	builder := transcript.NewBuilder()
	builder.AddUser("old prompt")
	builder.StartTurn("old", "")
	builder.AppendAssistant("old", "", "old partial")
	if err := store.UpdateActiveTranscript(t.Context(), transcriptDBUpdate(t, "continue", workspaceRoot, 0, builder.Thread())); err != nil {
		t.Fatal(err)
	}
	ui := tui.New()
	ui.SetLiveRunner(engine.NewLiveRunner(nil, workspace, "test-model"))
	ui.SetSettings(engine.NewSettings(store, nil, nil, workspaceRoot))
	if err := ui.ResumeSavedSession(t.Context(), "continue"); err != nil {
		t.Fatal(err)
	}
	ui.AddPartialTranscript("new", "new prompt", "new answer")
	cmd := ui.ForceTranscriptPersist()
	if cmd == nil {
		t.Fatal("next turn returned no persistence command")
	}
	ui.Update(cmd())
	stored, err := store.GetSessionTranscript(t.Context(), "continue")
	if err != nil {
		t.Fatal(err)
	}
	text := tui.TranscriptText(stored)
	if !strings.Contains(text, "old partial") || !strings.Contains(text, "new answer") {
		t.Fatalf("continued transcript = %s", text)
	}
}

func TestInterruptedRecoveryWriteFailureLeavesUIAndTranscriptUnchanged(t *testing.T) {
	workspaceRoot := t.TempDir()
	workspace, err := code.NewWorkspace(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(t.TempDir(), "sessions.db")
	store, err := db.Open(t.Context(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	builder := transcript.NewBuilder()
	builder.AddUser("saved prompt")
	builder.StartTurn("turn", "")
	builder.AppendAssistant("turn", "", "saved partial")
	before := builder.Thread()
	if err := store.UpdateActiveTranscript(t.Context(), transcriptDBUpdate(t, "recovery-fails", workspaceRoot, 0, before)); err != nil {
		t.Fatal(err)
	}
	blocker, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blocker.Close() })
	if _, err := blocker.ExecContext(t.Context(), `CREATE TRIGGER reject_recovery BEFORE UPDATE ON session_transcripts BEGIN SELECT RAISE(ABORT, 'recovery blocked'); END`); err != nil {
		t.Fatal(err)
	}
	ui := tui.New()
	ui.SetLiveRunner(engine.NewLiveRunner(nil, workspace, "test-model"))
	ui.SetSettings(engine.NewSettings(store, nil, nil, workspaceRoot))
	ui.AddTranscriptMessages([]llm.Message{{Role: llm.RoleUser, Content: "current UI"}})
	err = ui.ResumeSavedSession(t.Context(), "recovery-fails")
	if err == nil || !strings.Contains(err.Error(), "persist interrupted transcript recovery") || !strings.Contains(err.Error(), "recovery blocked") {
		t.Fatalf("resume error = %v", err)
	}
	stored, err := store.GetSessionTranscript(t.Context(), "recovery-fails")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Revision != before.Revision() {
		t.Fatalf("failed recovery advanced revision: %d -> %d", before.Revision(), stored.Revision)
	}
	var model tea.Model = ui
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	out := ansi.Strip(model.View().Content)
	if !strings.Contains(out, "current UI") || strings.Contains(out, "saved partial") {
		t.Fatalf("failed recovery mutated UI:\n%s", out)
	}
}
