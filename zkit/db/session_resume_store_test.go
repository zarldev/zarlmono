package db_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
)

func TestSessionResumeStateDistinguishesMissingAndEmptyTranscript(t *testing.T) {
	store, checkpoint := checkpointStore(t)
	state, err := store.GetSessionResumeState(t.Context(), checkpoint.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Transcript != nil {
		t.Fatal("invented draft transcript")
	}
	saveCheckpoint(t, store, checkpoint)
	state, err = store.GetSessionResumeState(t.Context(), checkpoint.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Transcript == nil || state.Transcript.Revision != 0 {
		t.Fatal("lost present empty transcript")
	}
	if _, err := store.GetSessionResumeState(t.Context(), "missing"); !errors.Is(err, db.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestSessionResumeStateDoesNotTearCompletedTurn(t *testing.T) {
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	commit := func(revision uint64) error {
		return store.CommitCompletedTurn(t.Context(), db.SessionRecord{
			ID: "source", Workspace: "/workspace", ContextJSON: fmt.Appendf(nil, `[{"revision":%d}]`, revision),
		}, db.TranscriptUpdate{
			SessionID: "source", Workspace: "/workspace", Revision: revision, ExpectedRevision: revision - 1,
			Entries: []db.TranscriptEntry{{Sequence: revision, EntryID: strconv.FormatUint(revision, 10), Kind: "notice", PayloadJSON: []byte(`{"text":"saved"}`), Revision: revision}},
		})
	}
	if err := commit(1); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		for revision := uint64(2); revision <= 40; revision++ {
			if err := commit(revision); err != nil {
				t.Error(err)
				return
			}
		}
	})
	for range 80 {
		state, err := store.GetSessionResumeState(t.Context(), "source")
		if err != nil {
			t.Error(err)
			break
		}
		if state.Transcript == nil {
			t.Error("missing transcript")
			break
		}
		want := fmt.Sprintf(`[{"revision":%d}]`, state.Transcript.Revision)
		if string(state.Session.ContextJSON) != want {
			t.Errorf("torn resume at revision %d", state.Transcript.Revision)
		}
	}
	wg.Wait()
}

func TestSessionResumeStateIncludesValidatedBranchProvenance(t *testing.T) {
	t.Parallel()
	store, checkpoint := checkpointStore(t)
	checkpoint = saveCheckpoint(t, store, checkpoint)
	source, err := store.GetSessionResumeState(t.Context(), checkpoint.SessionID)
	if err != nil || source.Branch != nil {
		t.Fatalf("original provenance = %v, %v", source.Branch, err)
	}
	branch := emptyCheckpointBranch(checkpoint)
	if err := store.CreateCheckpointBranch(t.Context(), branch); err != nil {
		t.Fatal(err)
	}
	child, err := store.GetSessionResumeState(t.Context(), branch.Child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if child.Branch == nil || child.Branch.SourceSessionID != checkpoint.SessionID || child.Transcript == nil {
		t.Fatal("child resume lost provenance or canonical history")
	}
	if _, err := store.DB().ExecContext(t.Context(), "UPDATE session_branches SET checksum = 'corrupt'"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSessionResumeState(t.Context(), branch.Child.ID); !errors.Is(err, db.ErrCheckpointCorrupt) {
		t.Fatalf("corrupt branch resume = %v", err)
	}
}
