package tui_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestTranscriptPersistsBeforeContextSnapshotExists(t *testing.T) {
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

	ui := tui.New()
	ui.SetLiveRunner(engine.NewLiveRunner(nil, workspace, "test-model"))
	ui.SetSettings(engine.NewSettings(store, nil, nil, workspaceRoot))
	ui.SetSessionIdentity("mid-turn", "Mid turn", false, time.Now())
	ui.AddPartialTranscript("turn", "durable prompt", "partial response")

	cmd := ui.ForceTranscriptPersist()
	if cmd == nil {
		t.Fatal("transcript persistence returned no command")
	}
	msg := cmd()
	if _, next := ui.Update(msg); next != nil {
		t.Fatal("unexpected queued persistence operation")
	}
	stored, err := store.GetSessionTranscript(t.Context(), "mid-turn")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tui.TranscriptText(stored), "durable prompt") || !strings.Contains(tui.TranscriptText(stored), "partial response") {
		t.Fatalf("mid-turn transcript missing content: %s", tui.TranscriptText(stored))
	}
	record, err := store.GetSession(t.Context(), "mid-turn")
	if err != nil {
		t.Fatal(err)
	}
	if string(record.ContextJSON) != "[]" {
		t.Fatalf("mid-turn transcript write changed model context: %s", record.ContextJSON)
	}
}

func TestFlushAcknowledgesCompletedInFlightTranscriptBeforeFinalSnapshot(t *testing.T) {
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

	ui := tui.New()
	ui.SetLiveRunner(engine.NewLiveRunner(nil, workspace, "test-model"))
	ui.SetSettings(engine.NewSettings(store, nil, nil, workspaceRoot))
	ui.SetSessionIdentity("in-flight-shutdown", "In-flight shutdown", false, time.Now())
	ui.AddPartialTranscript("turn", "survive shutdown", "partial body")
	cmd := ui.StartTranscriptPersistWithoutDelivering()
	if cmd == nil {
		t.Fatal("transcript persistence returned no command")
	}
	cmd()

	if err := ui.FlushSessionPersistence(t.Context()); err != nil {
		t.Fatalf("flush after completed in-flight persistence: %v", err)
	}

	stored, err := store.GetSessionTranscript(t.Context(), "in-flight-shutdown")
	if err != nil {
		t.Fatal(err)
	}
	if ui.PersistedTranscriptRevision() != stored.Revision {
		t.Fatalf("ack revision = %d, want %d", ui.PersistedTranscriptRevision(), stored.Revision)
	}
}

func TestTranscriptDebounceSupersedesStaleGeneration(t *testing.T) {
	workspaceRoot := t.TempDir()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ui := tui.New()
	ui.SetSettings(engine.NewSettings(store, nil, nil, workspaceRoot))
	ui.SetSessionIdentity("debounce", "Debounce", false, time.Now())
	ui.AddPartialTranscript("turn", "prompt", "a")
	if ui.ScheduleTranscriptPersist() == nil {
		t.Fatal("first schedule returned nil")
	}
	stale := ui.TranscriptGeneration()
	ui.AddPartialTranscript("turn-2", "second", "b")
	if ui.ScheduleTranscriptPersist() == nil {
		t.Fatal("second schedule returned nil")
	}
	if cmd := ui.HandleTranscriptDebounce(stale); cmd != nil {
		t.Fatal("stale transcript debounce scheduled a write")
	}
	cmd := ui.HandleTranscriptDebounce(ui.TranscriptGeneration())
	if cmd == nil {
		t.Fatal("current transcript debounce returned nil")
	}
	msg := cmd()
	ui.Update(msg)
	stored, err := store.GetSessionTranscript(t.Context(), "debounce")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tui.TranscriptText(stored), "second") {
		t.Fatalf("debounced transcript missing latest mutation: %s", tui.TranscriptText(stored))
	}
}

func TestFlushSessionPersistenceWritesQueuedPartialTranscript(t *testing.T) {
	workspaceRoot := t.TempDir()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ui := tui.New()
	ui.SetSettings(engine.NewSettings(store, nil, nil, workspaceRoot))
	ui.SetSessionIdentity("shutdown", "Shutdown", false, time.Now())
	ui.AddPartialTranscript("turn", "survive shutdown", "partial body")
	ui.QueueTranscriptPersist()
	if err := ui.FlushSessionPersistence(t.Context()); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetSessionTranscript(t.Context(), "shutdown")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tui.TranscriptText(stored), "survive shutdown") || !strings.Contains(tui.TranscriptText(stored), "partial body") {
		t.Fatalf("shutdown flush lost partial transcript: %s", tui.TranscriptText(stored))
	}
}

func TestMidTurnTranscriptPersistPreservesCompletedContext(t *testing.T) {
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
	const sessionID = "existing-context"
	contextJSON := []byte(`[{"role":"user","content":"completed context"}]`)
	if err := store.SaveSession(t.Context(), db.SessionRecord{
		ID: sessionID, Workspace: workspaceRoot, ContextJSON: contextJSON,
		PendingJSON:   []byte(`[{"kind":"text","text":"keep draft"}]`),
		LastUsageJSON: []byte(`{"input_tokens":9}`), PlanJSON: []byte(`{"steps":[]}`),
	}); err != nil {
		t.Fatal(err)
	}
	ui := tui.New()
	ui.SetLiveRunner(engine.NewLiveRunner(nil, workspace, "test-model"))
	ui.SetSettings(engine.NewSettings(store, nil, nil, workspaceRoot))
	ui.SetSessionIdentity(sessionID, "Existing", false, time.Now())
	ui.AddPartialTranscript("turn", "next prompt", "next partial answer")
	cmd := ui.ForceTranscriptPersist()
	if cmd == nil {
		t.Fatal("transcript persistence returned no command")
	}
	ui.Update(cmd())
	got, err := store.GetSession(t.Context(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.ContextJSON) != string(contextJSON) {
		t.Fatalf("mid-turn write changed completed context: %s", got.ContextJSON)
	}
	if string(got.PendingJSON) != `[{"kind":"text","text":"keep draft"}]` || string(got.LastUsageJSON) != `{"input_tokens":9}` || string(got.PlanJSON) != `{"steps":[]}` {
		t.Fatalf("mid-turn write changed unrelated state: %#v", got)
	}
}

func TestQueuedTranscriptGenerationsCoalesce(t *testing.T) {
	workspaceRoot := t.TempDir()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ui := tui.New()
	ui.SetSettings(engine.NewSettings(store, nil, nil, workspaceRoot))
	ui.SetSessionIdentity("coalesce", "Coalesce", false, time.Now())
	ui.AddPartialTranscript("one", "first", "a")
	ui.QueueLatestTranscriptPersist()
	ui.AddPartialTranscript("two", "latest", "b")
	ui.QueueLatestTranscriptPersist()
	kinds := ui.SessionPersistQueueKinds()
	if len(kinds) != 1 || kinds[0] != "transcript" {
		t.Fatalf("queued operations = %v, want one transcript", kinds)
	}
	if err := ui.FlushSessionPersistence(t.Context()); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetSessionTranscript(t.Context(), "coalesce")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tui.TranscriptText(stored), "latest") {
		t.Fatalf("coalesced transcript is stale: %s", tui.TranscriptText(stored))
	}
}

func TestDeleteBarrierDropsQueuedSessionWrites(t *testing.T) {
	workspaceRoot := t.TempDir()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpdateActiveTranscript(t.Context(), db.TranscriptUpdate{
		SessionID: "delete", Workspace: workspaceRoot,
		Revision: 1,
		Entries:  []db.TranscriptEntry{{Sequence: 1, EntryID: "e1", Kind: "user_message", PayloadJSON: []byte(`{"text":"delete me"}`), Revision: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	ui := tui.New()
	ui.SetSettings(engine.NewSettings(store, nil, nil, workspaceRoot))
	ui.SetSessionIdentity("delete", "Delete", false, time.Now())
	ui.AddPartialTranscript("turn", "do not recreate", "partial")
	ui.QueueLatestTranscriptPersist()
	ui.QueueDeletePersist("delete")
	kinds := ui.SessionPersistQueueKinds()
	if len(kinds) != 1 || kinds[0] != "delete" {
		t.Fatalf("queued operations = %v, want delete barrier", kinds)
	}
	if err := ui.FlushSessionPersistence(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSession(t.Context(), "delete"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("session after delete error = %v, want ErrNotFound", err)
	}
}

func TestFlushWaitsForInFlightPersistence(t *testing.T) {
	ui := tui.New()
	started := make(chan struct{})
	release := make(chan struct{})
	cmd := ui.StartBlockingTranscriptPersist(started, release, nil)
	commandDone := make(chan struct{})
	go func() {
		defer close(commandDone)
		cmd()
	}()
	<-started
	flushDone := make(chan error, 1)
	go func() { flushDone <- ui.FlushSessionPersistence(t.Context()) }()
	select {
	case err := <-flushDone:
		t.Fatalf("flush returned before in-flight write completed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-flushDone; err != nil {
		t.Fatal(err)
	}
	<-commandDone
}

func TestFlushReportsInFlightPersistenceError(t *testing.T) {
	ui := tui.New()
	started := make(chan struct{})
	release := make(chan struct{})
	cmd := ui.StartBlockingTranscriptPersist(started, release, errors.New("disk unavailable"))
	go cmd()
	<-started
	close(release)
	err := ui.FlushSessionPersistence(t.Context())
	if err == nil || !strings.Contains(err.Error(), "disk unavailable") {
		t.Fatalf("flush error = %v, want disk unavailable", err)
	}
}

func TestFlushTimeoutWhilePersistenceIsInFlight(t *testing.T) {
	ui := tui.New()
	started := make(chan struct{})
	release := make(chan struct{})
	cmd := ui.StartBlockingTranscriptPersist(started, release, nil)
	commandDone := make(chan struct{})
	go func() {
		defer close(commandDone)
		cmd()
	}()
	<-started
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	err := ui.FlushSessionPersistence(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("flush error = %v, want deadline exceeded", err)
	}
	close(release)
	<-commandDone
}

func TestCompletedTurnBarrierDropsStaleTranscriptGeneration(t *testing.T) {
	ui := tui.New()
	ui.SetSessionIdentity("barrier", "Barrier", false, time.Now())
	ui.AddPartialTranscript("turn", "prompt", "partial")
	ui.QueueLatestTranscriptPersist()
	barrierGeneration := ui.TranscriptGeneration()
	ui.QueueCommitBarrier()
	ui.QueueTranscriptGeneration(barrierGeneration)
	kinds := ui.SessionPersistQueueKinds()
	if len(kinds) != 1 || kinds[0] != "commit" {
		t.Fatalf("queued operations = %v, want only commit barrier", kinds)
	}
}

func TestStreamingTranscriptPersistsOnlyChangedAssistantEntry(t *testing.T) {
	workspaceRoot := t.TempDir()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ui := tui.New()
	ui.SetSettings(engine.NewSettings(store, nil, nil, workspaceRoot))
	ui.SetSessionIdentity("incremental", "Incremental", false, time.Now())
	ui.AddPartialTranscript("turn", "fixed prompt", "a")
	cmd := ui.ForceTranscriptPersist()
	ui.Update(cmd())
	first, err := store.GetSessionTranscript(t.Context(), "incremental")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Entries) != 2 {
		t.Fatalf("first entries = %d", len(first.Entries))
	}
	ui.AddPartialTranscript("other", "second prompt", "b")
	cmd = ui.ForceTranscriptPersist()
	ui.Update(cmd())
	second, err := store.GetSessionTranscript(t.Context(), "incremental")
	if err != nil {
		t.Fatal(err)
	}
	if second.Revision <= first.Revision || len(second.Entries) != 4 {
		t.Fatalf("second transcript = %#v", second)
	}
	if string(second.Entries[0].PayloadJSON) != string(first.Entries[0].PayloadJSON) || second.Entries[0].Revision != first.Entries[0].Revision {
		t.Fatalf("incremental write changed stable entry: before=%#v after=%#v", first.Entries[0], second.Entries[0])
	}
	if ui.PersistedTranscriptRevision() != second.Revision {
		t.Fatalf("ack revision = %d, want %d", ui.PersistedTranscriptRevision(), second.Revision)
	}
}
