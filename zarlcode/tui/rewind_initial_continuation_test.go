package tui_test

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
)

func TestInitialContinuationBeforeCheckpointAndRepeatedRewind(t *testing.T) {
	f, id, _ := initialRewindChild(t)
	branch, err := f.store.GetSessionBranch(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	f.provider.check = func(ctx context.Context) {
		called = true
		candidates, err := f.store.ListSessionCheckpoints(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range candidates {
			record, err := f.store.GetSessionCheckpoint(ctx, id, candidate.ID)
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
			if snapshot.Boundary.PromptText != "first child turn" {
				continue
			}
			if snapshot.Transcript.Revision() == 0 || len(snapshot.Context) == 0 || snapshot.Boundary.SettledTurnID != "" || snapshot.Boundary.EventWatermark != 0 || !snapshot.Boundary.InitialContinuation.Matches(branch) {
				t.Fatal("initial continuation BEFORE lost its explicit provenance")
			}
			return
		}
		t.Fatal("provider admitted without durable child BEFORE")
	}
	settleRewindTurn(t, f, "first child turn")
	if !called {
		t.Fatal("child turn was not dispatched")
	}
	previewRewindPrompt(f, 1)
	_, cmd := f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	driveBeforeCommand(f, cmd)
	grandchild, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
	if err != nil || grandchild == id {
		t.Fatalf("repeat rewind did not branch: %v", err)
	}
	state, err := f.store.GetSessionResumeState(t.Context(), grandchild)
	if err != nil {
		t.Fatal(err)
	}
	head, err := rewind.DecodeResume(state.Session.ContextJSON)
	if err != nil || state.Branch == nil || !head.InitialContinuation.Matches(*state.Branch) {
		t.Fatalf("repeat rewind lost its own branch provenance: %v", err)
	}
	if f.ui.ComposerText() != "first child turn" {
		t.Fatal("repeat rewind submitted or lost prefill")
	}
}
