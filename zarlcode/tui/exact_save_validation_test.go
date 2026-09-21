package tui_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestExactSaveRejectsUnfinishedHistoricalEntries(t *testing.T) {
	f := newBeforeFixture(t)
	f.provider.check = func(context.Context) {}
	settleRewindTurn(t, f, "first")
	id := f.ui.SessionIdentity()
	before, err := f.store.GetSessionResumeState(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	f.ui.AddPartialTranscript("unfinished", "synthetic prompt", "PRIVATE-canary-answer")
	f.ui.Update(teasink.ThinkingMsg{TaskID: "unfinished", Delta: "PRIVATE-canary-reasoning"})
	f.ui.AddTranscriptMessages([]llm.Message{{Role: llm.RoleUser, Content: "later prompt"}, {Role: llm.RoleAssistant, Content: "later completed answer"}})
	if err := f.ui.SaveSession(t.Context()); !errors.Is(err, transcript.ErrCheckpointUnsettled) || !errors.Is(err, rewind.ErrInvalid) || strings.Contains(err.Error(), "PRIVATE-canary") {
		t.Fatalf("save rejection = %v", err)
	}
	after, err := f.store.GetSessionResumeState(t.Context(), id)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("rejected save changed durable head", err)
	}
	// Synthesize the historical defect through the storage boundary: revisions
	// match, the final entry is terminal, but earlier streams were never settled.
	records, err := f.ui.CanonicalThread().RecordsSince(before.Transcript.Revision)
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]db.TranscriptEntry, len(records))
	for i, r := range records {
		entries[i] = db.TranscriptEntry{Sequence: r.Sequence, EntryID: r.ID, ParentID: r.ParentID, TurnID: r.TurnID, Kind: r.Kind, Revision: r.Revision, PayloadJSON: r.Payload}
	}
	state, err := rewind.DecodeResume(before.Session.ContextJSON)
	if err != nil {
		t.Fatal(err)
	}
	record := before.Session
	revision := f.ui.CanonicalThread().Revision()
	record.ContextJSON, err = rewind.EncodeResume(revision, state.Context, state.Target, state.SettledTurnID, revision, rewind.InitialContinuation{})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.CommitCompletedTurn(t.Context(), record, db.TranscriptUpdate{SessionID: id, Workspace: f.ws.Root(), ExpectedRevision: before.Transcript.Revision, Revision: revision, Entries: entries}); err != nil {
		t.Fatal(err)
	}
	broken, err := f.store.GetSessionResumeState(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	ui := tui.New()
	ui.SetSettings(engine.NewSettings(f.store, nil, nil, f.ws.Root()))
	if err := ui.ResumeSavedSession(t.Context(), id); !errors.Is(err, rewind.ErrInvalid) || !errors.Is(err, transcript.ErrCheckpointUnsettled) || strings.Contains(err.Error(), "PRIVATE-canary") {
		t.Fatalf("resume rejection = %v", err)
	}
	retained, err := f.store.GetSessionResumeState(t.Context(), id)
	if err != nil || !reflect.DeepEqual(broken, retained) {
		t.Fatal("strict rejection repaired source", err)
	}
}

func TestNestedConversationTerminalSettlesCanonicalStreams(t *testing.T) {
	for _, reason := range []runner.TerminalReason{runner.TerminalCompleted, runner.TerminalError, runner.TerminalCancelled, runner.TerminalMaxIterations} {
		t.Run(string(reason), func(t *testing.T) {
			ui := tui.New()
			ui.Update(teasink.ConversationStartedMsg{TaskID: "root", Prompt: "synthetic task"})
			ui.Update(teasink.ConversationStartedMsg{TaskID: "child", Depth: 1, AgentName: "worker", Prompt: "synthetic child"})
			ui.Update(teasink.ContentMsg{TaskID: "child", Depth: 1, Delta: "child answer"})
			ui.Update(teasink.ThinkingMsg{TaskID: "child", Depth: 1, Delta: "child reasoning"})
			ui.Update(teasink.ConversationEndedMsg{TaskID: "child", Depth: 1, Reason: reason})
			ui.Update(teasink.ConversationEndedMsg{TaskID: "root", Reason: runner.TerminalCompleted})
			if _, err := ui.CanonicalThread().CaptureCheckpoint(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
