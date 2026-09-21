package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
)

func historyCounts(t *testing.T, store *db.Store) (int64, int64, int64) {
	t.Helper()
	var nodes, values, pins int64
	for _, item := range []struct {
		query string
		into  *int64
	}{
		{"SELECT count(*) FROM session_history_nodes", &nodes},
		{"SELECT count(*) FROM session_history_values", &values},
		{"SELECT count(*) FROM session_history_pins", &pins},
	} {
		if err := store.DB().QueryRowContext(t.Context(), item.query).Scan(item.into); err != nil {
			t.Fatal(err)
		}
	}
	return nodes, values, pins
}

func TestHistoryGCCapturePinLifecycle(t *testing.T) {
	for _, publish := range []bool{false, true} {
		name := "release"
		if publish {
			name = "publish"
		}
		t.Run(name, func(t *testing.T) {
			store, checkpoint := historyStore(t)
			ref, err := store.CaptureSessionHistory(t.Context(), "source", []db.TranscriptEntry{{EntryID: "new", PayloadJSON: []byte(`{"text":"held"}`)}}, []byte(`{"new":true}`))
			if err != nil {
				t.Fatal(err)
			}
			if ref.PinID == "" {
				t.Fatal("capture has no pin")
			}
			beforeNodes, beforeValues, pins := historyCounts(t, store)
			if pins != 1 {
				t.Fatalf("pins = %d", pins)
			}
			result, err := store.GarbageCollectHistory(t.Context())
			if err != nil || result != (db.HistoryGCResult{}) {
				t.Fatalf("live pin collected: %+v, %v", result, err)
			}
			if publish {
				checkpoint.ID = "publish"
				checkpoint.History = ref
				saved := saveCheckpoint(t, store, checkpoint)
				if saved.History.PinID != "" {
					t.Fatal("pin became immutable identity")
				}
			} else {
				// A failed publication must not consume its provisional root.
				checkpoint.History = ref
				if err := store.SaveSessionCheckpoint(t.Context(), checkpoint); !errors.Is(err, db.ErrCheckpointConflict) {
					t.Fatalf("publication = %v", err)
				}
				_, _, pins = historyCounts(t, store)
				if pins != 1 {
					t.Fatal("failed publication consumed pin")
				}
				if err := store.ReleaseCapturedHistory(t.Context(), ref); err != nil {
					t.Fatal(err)
				}
				if err := store.ReleaseCapturedHistory(t.Context(), ref); err != nil {
					t.Fatal(err)
				}
			}
			_, _, pins = historyCounts(t, store)
			if pins != 0 {
				t.Fatalf("settled pins = %d", pins)
			}
			if err := store.DeleteSession(t.Context(), "source"); err != nil {
				t.Fatal(err)
			}
			result, err = store.GarbageCollectHistory(t.Context())
			if err != nil || result.NodesDeleted != beforeNodes || result.ValuesDeleted != beforeValues {
				t.Fatalf("final reclamation = %+v, %v; want %d/%d", result, err, beforeNodes, beforeValues)
			}
			if _, _, _, err := store.ReadCheckpointHistory(t.Context(), ref); !errors.Is(err, db.ErrCheckpointUnavailable) {
				t.Fatalf("reclaimed read = %v", err)
			}
		})
	}
}

func TestHistoryGCSharedBranchSurvivesSourceDeletion(t *testing.T) {
	store, checkpoint := historyStore(t)
	if err := store.AppendSessionReplay(t.Context(), "source", [][]byte{[]byte(`{"shared":1}`)}); err != nil {
		t.Fatal(err)
	}
	ref, err := store.CaptureSessionHistory(t.Context(), "source", nil, []byte(`{"state":1}`))
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.ID, checkpoint.History = "fork", ref
	checkpoint = saveCheckpoint(t, store, checkpoint)
	branch := emptyCheckpointBranch(checkpoint)
	branch.ReplaySuffix = [][]byte{[]byte(`{"child":1}`)}
	if err := store.CreateCheckpointBranch(t.Context(), branch); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendSessionReplay(t.Context(), "source", [][]byte{[]byte(`{"source":1}`)}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSession(t.Context(), "source"); err != nil {
		t.Fatal(err)
	}
	result, err := store.GarbageCollectHistory(t.Context())
	if err != nil || result.NodesDeleted != 1 {
		t.Fatalf("source suffix reclamation = %+v, %v", result, err)
	}
	replay, err := store.ReadSessionReplay(t.Context(), "child")
	if err != nil || len(replay) != 2 {
		t.Fatalf("branch prefix/suffix = %d, %v", len(replay), err)
	}
	if _, _, _, err := store.ReadCheckpointHistory(t.Context(), checkpoint.History); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryGCAllRootKinds(t *testing.T) {
	for _, root := range []string{"session", "checkpoint-replay", "checkpoint-transcript", "model-context", "state"} {
		t.Run(root, func(t *testing.T) {
			store, checkpoint := historyStore(t)
			ref, err := store.CaptureSessionHistory(t.Context(), "source", []db.TranscriptEntry{{EntryID: "root", PayloadJSON: []byte(`{}`)}}, []byte(`{"direct":true}`))
			if err != nil {
				t.Fatal(err)
			}
			if err := store.ReleaseCapturedHistory(t.Context(), ref); err != nil {
				t.Fatal(err)
			}
			// Isolate one root class using fixture-only SQL; production never rewrites roots this way.
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := store.DB().ExecContext(t.Context(), query, args...); err != nil {
					t.Fatal(err)
				}
			}
			exec("DELETE FROM session_history_heads")
			exec("DELETE FROM session_checkpoint_history")
			switch root {
			case "session":
				exec("INSERT INTO session_history_heads VALUES (?, ?, ?)", "source", "replay", ref.TranscriptHead)
			case "checkpoint-replay":
				exec("INSERT INTO session_checkpoint_history VALUES (?, ?, '', ?, ?)", "source", checkpoint.ID, ref.TranscriptHead, ref.StateID)
			case "checkpoint-transcript":
				exec("INSERT INTO session_checkpoint_history VALUES (?, ?, ?, '', ?)", "source", checkpoint.ID, ref.TranscriptHead, ref.StateID)
			case "model-context":
				exec("INSERT INTO session_model_context VALUES (?, ?, 1, '{}')", "source", ref.TranscriptHead)
			case "state":
				exec("INSERT INTO session_checkpoint_history VALUES (?, ?, '', '', ?)", "source", checkpoint.ID, ref.StateID)
			}
			if _, err := store.GarbageCollectHistory(t.Context()); err != nil {
				t.Fatal(err)
			}
			nodes, values, _ := historyCounts(t, store)
			wantNodes, wantValues := int64(1), int64(1)
			if root == "state" {
				wantNodes = 0
			}
			if root == "checkpoint-replay" || root == "checkpoint-transcript" {
				wantValues = 2
			}
			if nodes != wantNodes || values != wantValues {
				t.Fatalf("remaining %d/%d; want %d/%d", nodes, values, wantNodes, wantValues)
			}
		})
	}
}

func TestHistoryGCCorruptionAndCancellationAreAtomic(t *testing.T) {
	for _, failure := range []string{"missing-node", "cycle", "corrupt-value", "cancelled", "delete-error"} {
		t.Run(failure, func(t *testing.T) {
			store, checkpoint := historyStore(t)
			ref, err := store.CaptureSessionHistory(t.Context(), "source", []db.TranscriptEntry{{EntryID: "orphan", PayloadJSON: []byte(`{}`)}}, []byte(`{"orphan":true}`))
			if err != nil {
				t.Fatal(err)
			}
			if err := store.ReleaseCapturedHistory(t.Context(), ref); err != nil {
				t.Fatal(err)
			}
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := store.DB().ExecContext(t.Context(), query, args...); err != nil {
					t.Fatal(err)
				}
			}
			ctx := t.Context()
			want := db.ErrCheckpointCorrupt
			switch failure {
			case "missing-node":
				exec("UPDATE session_history_heads SET head = 'missing' WHERE kind = 'replay'")
				want = db.ErrCheckpointUnavailable
			case "cycle":
				exec("INSERT INTO session_history_nodes VALUES ('cycle', 'cycle', ?)", checkpoint.History.StateID)
				exec("UPDATE session_history_heads SET head = 'cycle' WHERE kind = 'replay'")
			case "corrupt-value":
				exec("DROP TRIGGER session_history_values_immutable")
				exec("UPDATE session_history_values SET payload = 'corrupt' WHERE id = ?", checkpoint.History.StateID)
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
			case "delete-error":
				exec("CREATE TRIGGER reject_gc BEFORE DELETE ON session_history_values BEGIN SELECT RAISE(ABORT, 'test delete'); END")
			}
			n, v, p := historyCounts(t, store)
			result, err := store.GarbageCollectHistory(ctx)
			if err == nil || (failure != "delete-error" && !errors.Is(err, want)) || result != (db.HistoryGCResult{}) {
				t.Fatalf("GC = %+v, %v", result, err)
			}
			nn, vv, pp := historyCounts(t, store)
			if n != nn || v != vv || p != pp {
				t.Fatal("failed GC partially deleted history")
			}
		})
	}
}
