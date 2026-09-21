package db_test

import (
	"bytes"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
)

func historyStore(t *testing.T) (*db.Store, db.SessionCheckpoint) {
	t.Helper()
	store, checkpoint := checkpointStore(t)
	ref, err := store.CaptureSessionHistory(t.Context(), checkpoint.SessionID, nil, []byte(`{"boundary":0}`))
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.History = ref
	return store, saveCheckpoint(t, store, checkpoint)
}

func TestSessionHistorySharedPrefixAndIndependentSuffix(t *testing.T) {
	t.Parallel()
	store, checkpoint := historyStore(t)
	ctx := t.Context()
	repeated := []byte(`{"message":"same occurrence bytes"}`)
	if err := store.AppendSessionReplay(ctx, "source", [][]byte{repeated, repeated}); err != nil {
		t.Fatal(err)
	}
	ref, err := store.CaptureSessionHistory(ctx, "source", nil, []byte(`{"boundary":2}`))
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.ID = "fork"
	checkpoint.History = ref
	checkpoint = saveCheckpoint(t, store, checkpoint)
	branch := emptyCheckpointBranch(checkpoint)
	branch.ReplaySuffix = [][]byte{[]byte(`{"message":"child notice"}`)}
	if err := store.CreateCheckpointBranch(ctx, branch); err != nil {
		t.Fatal(err)
	}
	copied, err := store.GetSessionCheckpoint(ctx, "child", checkpoint.ID)
	// Publication consumes the provisional pin; branch identity is only the
	// immutable prefixes and state, never that transient capture ownership.
	ref.PinID = ""
	if err != nil || copied.History != ref {
		t.Fatalf("branch did not share prefix: %v", err)
	}
	if err := store.AppendSessionReplay(ctx, "source", [][]byte{[]byte(`{"message":"source suffix"}`)}); err != nil {
		t.Fatal(err)
	}
	child, err := store.CaptureSessionHistory(ctx, "child", nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_, messages, _, err := store.ReadCheckpointHistory(ctx, child)
	if err != nil || len(messages) != 3 || !bytes.Equal(messages[0], repeated) || !bytes.Equal(messages[1], repeated) || !bytes.Equal(messages[2], branch.ReplaySuffix[0]) {
		t.Fatalf("child occurrences: %s, %v", messages, err)
	}
	if err := store.DeleteSession(ctx, "source"); err != nil {
		t.Fatal(err)
	}
	_, messages, _, err = store.ReadCheckpointHistory(ctx, copied.History)
	if err != nil || len(messages) != 2 {
		t.Fatalf("source deletion removed shared history: %v", err)
	}
}

func TestSessionHistoryImmutableProjectionAndModelBoundary(t *testing.T) {
	t.Parallel()
	store, _ := historyStore(t)
	ctx := t.Context()
	entry := db.TranscriptEntry{Sequence: 1, EntryID: "entry", Revision: 1, PayloadJSON: []byte(`{"text":"original"}`)}
	before, err := store.CaptureSessionHistory(ctx, "source", []db.TranscriptEntry{entry}, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	entry.PayloadJSON = []byte(`{"text":"changed"}`)
	after, err := store.CaptureSessionHistory(ctx, "source", []db.TranscriptEntry{entry}, []byte(`{}`))
	if err != nil || before.TranscriptHead == after.TranscriptHead {
		t.Fatalf("mutable historical reference: %v", err)
	}
	entries, _, _, err := store.ReadCheckpointHistory(ctx, before)
	if err != nil || string(entries[0].PayloadJSON) != `{"text":"original"}` {
		t.Fatalf("overwrote old occurrence: %v", err)
	}
	request := []byte(`{"messages":[{"role":"system","content":"prepared"}]}`)
	for generation := uint64(1); generation <= 2; generation++ {
		if err := store.AppendSessionReplay(ctx, "source", [][]byte{[]byte(`{"message":"input"}`)}); err != nil {
			t.Fatal(err)
		}
		boundary, err := store.CaptureSessionHistory(ctx, "source", nil, []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SaveSessionModelContext(ctx, "source", request); err != nil {
			t.Fatal(err)
		}
		saved, err := store.GetSessionModelContext(ctx, "source")
		if err != nil || saved.Generation != generation || saved.HistoryHead != boundary.ReplayHead || !bytes.Equal(saved.RequestJSON, request) {
			t.Fatalf("model context boundary: %#v, %v", saved, err)
		}
	}
}

func TestSessionHistoryConcurrentAppendAndCorruption(t *testing.T) {
	t.Parallel()
	store, _ := historyStore(t)
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			if err := store.AppendSessionReplay(t.Context(), "source", [][]byte{[]byte(`{"same":true}`)}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	ref, err := store.CaptureSessionHistory(t.Context(), "source", nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_, messages, _, err := store.ReadCheckpointHistory(t.Context(), ref)
	if err != nil || len(messages) != 12 {
		t.Fatalf("lost occurrences: %d, %v", len(messages), err)
	}
	ref.ReplayHead = "missing"
	if _, _, _, err := store.ReadCheckpointHistory(t.Context(), ref); !errors.Is(err, db.ErrCheckpointUnavailable) {
		t.Fatalf("missing history: %v", err)
	}
	if _, err := store.DB().ExecContext(t.Context(), "UPDATE session_history_values SET payload = '{}' WHERE id = ?", ref.StateID); err == nil {
		t.Fatal("immutable values accepted mutation")
	}
	before, err := store.GetSessionModelContext(t.Context(), "source")
	if !errors.Is(err, db.ErrNotFound) || !reflect.DeepEqual(before, db.ModelContext{}) {
		t.Fatalf("invented model context: %v", err)
	}
}
