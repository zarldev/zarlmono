package db_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/db"
)

func headlessAuxiliaryEntry(attempt, ordinal int) db.HeadlessAuxiliaryProviderAccounting {
	return db.HeadlessAuxiliaryProviderAccounting{InvocationID: fmt.Sprintf("opaque-%d-%d", attempt, ordinal), Version: 1, PayloadJSON: []byte(`{"opaque":"settled"}`)}
}

func commitHeadlessAuxiliary(ctx context.Context, s *db.Store, start db.HeadlessRunStart, attempts []db.HeadlessAttemptRecord, entries []db.HeadlessAuxiliaryProviderAccounting, completion *db.HeadlessRunSummary) error {
	return s.CommitHeadlessRecordingWithAccountingBatch(ctx, start, attempts, db.HeadlessAccountingBatch{Auxiliaries: entries}, completion)
}

func TestHeadlessAuxiliaryProviderAccountingIdentitySnapshotsAndReopen(t *testing.T) {
	t.Parallel()
	s, path := headlessAccountingStore(t)
	start := headlessAccountingStart("opaque run ☃")
	entry := headlessAuxiliaryEntry(1, 1)
	want := entry.Clone()
	cloned := entry.Clone()
	cloned.PayloadJSON[0] = '!'
	if !bytes.Equal(entry.PayloadJSON, want.PayloadJSON) {
		t.Fatal("Clone aliases")
	}
	if err := commitHeadlessAuxiliary(t.Context(), s, start, nil, []db.HeadlessAuxiliaryProviderAccounting{entry, entry}, nil); err != nil {
		t.Fatal(err)
	}
	entry.PayloadJSON[0] = '!'
	first, err := s.GetHeadlessAuxiliaryProviderAccounting(t.Context(), start.ID, "opaque-1-1")
	if err != nil || first.Sequence == 0 || !reflect.DeepEqual(first.Accounting, want) {
		t.Fatalf("first: %+v %v", first, err)
	}
	first.Accounting.PayloadJSON[0] = '!'
	if err := commitHeadlessAuxiliary(t.Context(), s, start, nil, []db.HeadlessAuxiliaryProviderAccounting{want, headlessAuxiliaryEntry(2, 1), headlessAuxiliaryEntry(1, math.MaxInt)}, nil); err != nil {
		t.Fatal(err)
	}
	other := headlessAccountingStart("other")
	if err := commitHeadlessAuxiliary(t.Context(), s, other, nil, []db.HeadlessAuxiliaryProviderAccounting{want}, nil); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListHeadlessAuxiliaryProviderAccounting(t.Context(), start.ID, 0, 1)
	if err != nil || len(page) != 1 || !reflect.DeepEqual(page[0].Accounting, want) {
		t.Fatalf("page: %+v %v", page, err)
	}
	seq := page[0].Sequence
	page[0].Accounting.PayloadJSON[0] = '!'
	rest, err := s.ListHeadlessAuxiliaryProviderAccounting(t.Context(), start.ID, seq, 256)
	if err != nil || len(rest) != 2 || rest[0].Sequence <= seq || rest[1].Sequence <= rest[0].Sequence {
		t.Fatalf("rest: %+v %v", rest, err)
	}
	if _, err := s.GetHeadlessAuxiliaryProviderAccounting(t.Context(), "absent", "opaque-1-1"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("scope: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	got, err := reopened.GetHeadlessAuxiliaryProviderAccounting(t.Context(), start.ID, "opaque-1-1")
	if err != nil || got.Sequence != seq || !reflect.DeepEqual(got.Accounting, want) {
		t.Fatalf("reopen: %+v %v", got, err)
	}
	if _, err := reopened.DB().ExecContext(t.Context(), "DELETE FROM headless_runs WHERE id = ?", start.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := reopened.ListHeadlessAuxiliaryProviderAccounting(t.Context(), start.ID, 0, 256); err != nil || len(got) != 0 {
		t.Fatalf("cascade: %+v %v", got, err)
	}
	if _, err := reopened.GetHeadlessAuxiliaryProviderAccounting(t.Context(), other.ID, "opaque-1-1"); err != nil {
		t.Fatal(err)
	}
}

func TestHeadlessAuxiliaryProviderAccountingAtomicRetryAndFrozenStart(t *testing.T) {
	s, _ := headlessAccountingStore(t)
	start := headlessAccountingStart("run")
	zero := int64(0)
	attempt := db.HeadlessAttemptRecord{RunID: start.ID, AttemptNumber: 1, FinalContent: "first", TokensIn: &zero}
	summary := db.HeadlessRunSummary{EndedAt: time.Unix(2222, 0), TerminalReason: "done", FinalContent: "complete", TokensOut: &zero}
	entry := headlessAuxiliaryEntry(1, 1)
	if err := commitHeadlessAuxiliary(t.Context(), s, start, []db.HeadlessAttemptRecord{attempt}, []db.HeadlessAuxiliaryProviderAccounting{entry}, &summary); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateHeadlessRunProgress(t.Context(), start.ID, 9, 7); err != nil {
		t.Fatal(err)
	}
	start.StartedAt = time.Unix(1234, 999999999)
	if err := commitHeadlessAuxiliary(t.Context(), s, start, nil, []db.HeadlessAuxiliaryProviderAccounting{entry}, nil); err != nil {
		t.Fatalf("second precision/progress: %v", err)
	}
	before, err := s.GetHeadlessRun(t.Context(), start.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"id", "workspace", "base", "prompt", "time", "provider", "model"} {
		t.Run(field, func(t *testing.T) {
			changed := start
			switch field {
			case "id":
				changed.ID = ""
			case "workspace":
				changed.Workspace = "changed"
			case "base":
				changed.BaseCommit = "changed"
			case "prompt":
				changed.Prompt = "changed"
			case "time":
				changed.StartedAt = time.Unix(1235, 0)
			case "provider":
				changed.Provider = "changed"
			case "model":
				changed.Model = "changed"
			}
			want := db.ErrHeadlessProviderAccountingConflict
			if field == "id" {
				want = db.ErrHeadlessProviderAccountingInvalidRequest
			}
			if err := commitHeadlessAuxiliary(t.Context(), s, changed, nil, nil, &db.HeadlessRunSummary{FinalContent: "bad"}); !errors.Is(err, want) {
				t.Fatalf("frozen start: %v", err)
			}
		})
	}
	attempt.FinalContent = "replaced"
	conflict := entry.Clone()
	conflict.PayloadJSON = []byte(`{"different":true}`)
	if err := commitHeadlessAuxiliary(t.Context(), s, start, []db.HeadlessAttemptRecord{attempt}, []db.HeadlessAuxiliaryProviderAccounting{headlessAuxiliaryEntry(1, 2), conflict}, &db.HeadlessRunSummary{FinalContent: "bad"}); !errors.Is(err, db.ErrHeadlessAuxiliaryProviderAccountingConflict) {
		t.Fatalf("conflict: %v", err)
	}
	after, err := s.GetHeadlessRun(t.Context(), start.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("summary partially changed: %v", err)
	}
	attempts, err := s.ListHeadlessAttempts(t.Context(), start.ID)
	if err != nil || len(attempts) != 1 || attempts[0].FinalContent != "first" || attempts[0].TokensIn == nil || *attempts[0].TokensIn != 0 {
		t.Fatalf("attempt changed: %+v %v", attempts, err)
	}
	if _, err := s.GetHeadlessAuxiliaryProviderAccounting(t.Context(), start.ID, "opaque-1-2"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("partial row: %v", err)
	}
	// Legacy UPSERT remains compatible and the new operation reuses that behavior.
	if err := s.InsertHeadlessAttempt(t.Context(), attempt); err != nil {
		t.Fatal(err)
	}
	attempt.FinalContent = "atomic latest"
	if err := commitHeadlessAuxiliary(t.Context(), s, start, []db.HeadlessAttemptRecord{attempt}, nil, &summary); err != nil {
		t.Fatal(err)
	}
	attempts, err = s.ListHeadlessAttempts(t.Context(), start.ID)
	if err != nil || len(attempts) != 1 || attempts[0].FinalContent != "atomic latest" || attempts[0].RecordedAt.IsZero() {
		t.Fatalf("upsert/default time: %+v %v", attempts, err)
	}
	// A duplicate conflict must roll back even the newly ensured start.
	fresh := headlessAccountingStart("fresh")
	if err := commitHeadlessAuxiliary(t.Context(), s, fresh, nil, []db.HeadlessAuxiliaryProviderAccounting{entry, conflict}, nil); !errors.Is(err, db.ErrHeadlessAuxiliaryProviderAccountingConflict) {
		t.Fatal(err)
	}
	if _, err := s.GetHeadlessRun(t.Context(), fresh.ID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("partial start: %v", err)
	}
}

func TestHeadlessAuxiliaryProviderAccountingCommitTimeRollback(t *testing.T) {
	t.Parallel()
	s, _ := headlessAccountingStore(t)
	// This deferred constraint is deliberately outside the production schema; all
	// writes execute successfully, but COMMIT must fail and expose no partial run.
	_, err := s.DB().ExecContext(t.Context(), `CREATE TABLE deferred_owner(id INTEGER PRIMARY KEY);
 CREATE TABLE deferred_child(owner INTEGER REFERENCES deferred_owner(id) DEFERRABLE INITIALLY DEFERRED);
 CREATE TRIGGER reject_accounting AFTER INSERT ON headless_auxiliary_provider_accounting BEGIN INSERT INTO deferred_child VALUES (99); END;`)
	if err != nil {
		t.Fatal(err)
	}
	start := headlessAccountingStart("commit-failure")
	err = commitHeadlessAuxiliary(t.Context(), s, start, []db.HeadlessAttemptRecord{{RunID: start.ID, AttemptNumber: 1}}, []db.HeadlessAuxiliaryProviderAccounting{headlessAuxiliaryEntry(1, 1)}, &db.HeadlessRunSummary{FinalContent: "bad"})
	if err == nil {
		t.Fatal("expected deferred commit failure")
	}
	if _, err := s.GetHeadlessRun(t.Context(), start.ID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("partial start: %v", err)
	}
	if attempts, err := s.ListHeadlessAttempts(t.Context(), start.ID); err != nil || len(attempts) != 0 {
		t.Fatalf("partial attempts: %v", err)
	}
	if rows, err := s.ListHeadlessAuxiliaryProviderAccounting(t.Context(), start.ID, 0, 256); err != nil || len(rows) != 0 {
		t.Fatalf("partial accounting: %v", err)
	}
}

func TestHeadlessAuxiliaryProviderAccountingBounds(t *testing.T) {
	s, _ := headlessAccountingStore(t)
	for _, tc := range []struct {
		name string
		edit func(*db.HeadlessRunStart, *db.HeadlessAuxiliaryProviderAccounting)
		want error
	}{
		{"empty scope", func(s *db.HeadlessRunStart, _ *db.HeadlessAuxiliaryProviderAccounting) { s.ID = "" }, db.ErrHeadlessProviderAccountingInvalidRequest},
		{"invalid scope UTF8", func(s *db.HeadlessRunStart, _ *db.HeadlessAuxiliaryProviderAccounting) { s.ID = string([]byte{255}) }, db.ErrHeadlessProviderAccountingInvalidRequest},
		{"empty invocation", func(_ *db.HeadlessRunStart, r *db.HeadlessAuxiliaryProviderAccounting) { r.InvocationID = "" }, db.ErrHeadlessAuxiliaryProviderAccountingInvalidRequest},
		{"invalid invocation UTF8", func(_ *db.HeadlessRunStart, r *db.HeadlessAuxiliaryProviderAccounting) {
			r.InvocationID = string([]byte{255})
		}, db.ErrHeadlessAuxiliaryProviderAccountingInvalidRequest},
		{"zero version", func(_ *db.HeadlessRunStart, r *db.HeadlessAuxiliaryProviderAccounting) { r.Version = 0 }, db.ErrHeadlessAuxiliaryProviderAccountingVersion},
		{"large version", func(_ *db.HeadlessRunStart, r *db.HeadlessAuxiliaryProviderAccounting) { r.Version = math.MaxUint64 }, db.ErrHeadlessAuxiliaryProviderAccountingVersion},
		{"empty payload", func(_ *db.HeadlessRunStart, r *db.HeadlessAuxiliaryProviderAccounting) { r.PayloadJSON = nil }, db.ErrHeadlessAuxiliaryProviderAccountingInvalidRequest},
		{"private payload", func(_ *db.HeadlessRunStart, r *db.HeadlessAuxiliaryProviderAccounting) {
			r.PayloadJSON = []byte("PRIVATE_CANARY")
		}, db.ErrHeadlessAuxiliaryProviderAccountingInvalidRequest},
		{"trailing", func(_ *db.HeadlessRunStart, r *db.HeadlessAuxiliaryProviderAccounting) {
			r.PayloadJSON = []byte(`{} {}`)
		}, db.ErrHeadlessAuxiliaryProviderAccountingInvalidRequest},
		{"UTF8", func(_ *db.HeadlessRunStart, r *db.HeadlessAuxiliaryProviderAccounting) {
			r.PayloadJSON = []byte{'"', 255, '"'}
		}, db.ErrHeadlessAuxiliaryProviderAccountingInvalidRequest},
		{"oversized", func(_ *db.HeadlessRunStart, r *db.HeadlessAuxiliaryProviderAccounting) {
			r.PayloadJSON = []byte(`"` + strings.Repeat("x", 1<<20) + `"`)
		}, db.ErrHeadlessAuxiliaryProviderAccountingInvalidRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := headlessAccountingStart(tc.name)
			r := headlessAuxiliaryEntry(1, 1)
			tc.edit(&start, &r)
			err := commitHeadlessAuxiliary(t.Context(), s, start, nil, []db.HeadlessAuxiliaryProviderAccounting{r}, nil)
			if !errors.Is(err, tc.want) || strings.Contains(err.Error(), "PRIVATE_CANARY") {
				t.Fatalf("input: %v", err)
			}
		})
	}
	for _, a := range []db.HeadlessAttemptRecord{{RunID: "other", AttemptNumber: 1}, {RunID: "run", AttemptNumber: 0}} {
		if err := commitHeadlessAuxiliary(t.Context(), s, headlessAccountingStart("run"), []db.HeadlessAttemptRecord{a}, nil, nil); !errors.Is(err, db.ErrHeadlessProviderAccountingInvalidRequest) {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		run    string
		cursor uint64
		limit  int
	}{{"", 0, 1}, {string([]byte{255}), 0, 1}, {"run", math.MaxUint64, 1}, {"run", 0, 0}, {"run", 0, 257}} {
		if _, err := s.ListHeadlessAuxiliaryProviderAccounting(t.Context(), tc.run, tc.cursor, tc.limit); !errors.Is(err, db.ErrHeadlessAuxiliaryProviderAccountingInvalidRequest) {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ run, invocation string }{{"", "x"}, {string([]byte{255}), "x"}, {"run", ""}, {"run", string([]byte{255})}} {
		if _, err := s.GetHeadlessAuxiliaryProviderAccounting(t.Context(), tc.run, tc.invocation); !errors.Is(err, db.ErrHeadlessAuxiliaryProviderAccountingInvalidRequest) {
			t.Fatal(err)
		}
	}
	// Inclusive payload bound, and opaque JSON scalar acceptance at the DB boundary.
	r := headlessAuxiliaryEntry(1, 1)
	r.PayloadJSON = []byte(`"` + strings.Repeat("x", (1<<20)-2) + `"`)
	if err := commitHeadlessAuxiliary(t.Context(), s, headlessAccountingStart("limit"), nil, []db.HeadlessAuxiliaryProviderAccounting{r}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestHeadlessAuxiliaryProviderAccountingCorruptWholePage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, sql string
		want      error
	}{
		{"checksum", `checksum='different'`, db.ErrHeadlessAuxiliaryProviderAccountingCorrupt},
		{"blob checksum", `checksum=X'6162'`, db.ErrHeadlessAuxiliaryProviderAccountingCorrupt},
		{"empty invocation", `invocation_id=''`, db.ErrHeadlessAuxiliaryProviderAccountingCorrupt},
		{"invalid UTF8 payload", `payload_json=X'22ff22'`, db.ErrHeadlessAuxiliaryProviderAccountingCorrupt},
		{"invalid JSON payload", `payload_json=X'7b'`, db.ErrHeadlessAuxiliaryProviderAccountingCorrupt},
		{"payload", `payload_json=X'7b7d'`, db.ErrHeadlessAuxiliaryProviderAccountingCorrupt},
		{"text payload", `payload_json='{}'`, db.ErrHeadlessAuxiliaryProviderAccountingCorrupt},
		{"oversized payload", `payload_json=zeroblob(1048577)`, db.ErrHeadlessAuxiliaryProviderAccountingCorrupt},
		{"zero version", `version=0`, db.ErrHeadlessAuxiliaryProviderAccountingCorrupt},
		{"version", `version=2`, db.ErrHeadlessAuxiliaryProviderAccountingVersion},
		{"real version", `version=1.5`, db.ErrHeadlessAuxiliaryProviderAccountingCorrupt},
		{"invocation", `invocation_id='changed'`, db.ErrHeadlessAuxiliaryProviderAccountingCorrupt},
		{"blob invocation", `invocation_id=X'626164'`, db.ErrHeadlessAuxiliaryProviderAccountingCorrupt},
		{"negative version", `version=-1`, db.ErrHeadlessAuxiliaryProviderAccountingCorrupt},
		{"blob version", `version=X'31'`, db.ErrHeadlessAuxiliaryProviderAccountingCorrupt},
		{"run transplant", `run_id='other'`, db.ErrHeadlessAuxiliaryProviderAccountingCorrupt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, _ := headlessAccountingStore(t)
			if err := commitHeadlessAuxiliary(t.Context(), s, headlessAccountingStart("run"), nil, []db.HeadlessAuxiliaryProviderAccounting{headlessAuxiliaryEntry(1, 1), headlessAuxiliaryEntry(1, 2)}, nil); err != nil {
				t.Fatal(err)
			}
			if err := s.InsertHeadlessRun(t.Context(), headlessAccountingStart("other")); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DB().ExecContext(t.Context(), "UPDATE headless_auxiliary_provider_accounting SET "+tc.sql+" WHERE invocation_id='opaque-1-2'"); err != nil {
				t.Fatal(err)
			}
			scope := "run"
			if tc.name == "run transplant" {
				scope = "other"
			}
			page, err := s.ListHeadlessAuxiliaryProviderAccounting(t.Context(), scope, 0, 256)
			if !errors.Is(err, tc.want) || page != nil {
				t.Fatalf("page must fail whole: %+v %v", page, err)
			}
			if tc.name != "invocation" && tc.name != "blob invocation" && tc.name != "empty invocation" {
				if _, err := s.GetHeadlessAuxiliaryProviderAccounting(t.Context(), scope, "opaque-1-2"); !errors.Is(err, tc.want) {
					t.Fatal("get integrity", err)
				}
				if err := commitHeadlessAuxiliary(t.Context(), s, headlessAccountingStart(scope), nil, []db.HeadlessAuxiliaryProviderAccounting{headlessAuxiliaryEntry(1, 2)}, nil); !errors.Is(err, tc.want) {
					t.Fatal("replay integrity", err)
				}
			}
		})
	}
}

func TestHeadlessAuxiliaryProviderAccountingIndependentStores(t *testing.T) {
	t.Parallel()
	s, path := headlessAccountingStore(t)
	other, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	for n := range 12 {
		start := headlessAccountingStart(string(rune('a' + n)))
		ready := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for _, store := range []*db.Store{s, other} {
			wg.Go(func() {
				<-ready
				results <- commitHeadlessAuxiliary(t.Context(), store, start, nil, []db.HeadlessAuxiliaryProviderAccounting{headlessAuxiliaryEntry(1, 1)}, nil)
			})
		}
		close(ready)
		wg.Wait()
		close(results)
		for err := range results {
			if err != nil {
				t.Fatalf("concurrent identical writer: %v", err)
			}
		}
	}
	start := headlessAccountingStart("conflict")
	changed := headlessAuxiliaryEntry(1, 1)
	changed.PayloadJSON = []byte(`{"changed":true}`)
	results := make(chan error, 2)
	var wg sync.WaitGroup
	ready := make(chan struct{})
	wg.Go(func() {
		<-ready
		results <- commitHeadlessAuxiliary(t.Context(), s, start, nil, []db.HeadlessAuxiliaryProviderAccounting{headlessAuxiliaryEntry(1, 1)}, nil)
	})
	wg.Go(func() {
		<-ready
		results <- commitHeadlessAuxiliary(t.Context(), other, start, nil, []db.HeadlessAuxiliaryProviderAccounting{changed}, nil)
	})
	close(ready)
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, db.ErrHeadlessAuxiliaryProviderAccountingConflict):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := commitHeadlessAuxiliary(ctx, s, headlessAccountingStart("cancel"), nil, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := s.GetHeadlessAuxiliaryProviderAccounting(ctx, start.ID, "opaque-1-1"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.ListHeadlessAuxiliaryProviderAccounting(ctx, start.ID, 0, 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestHeadlessAuxiliaryProviderAccountingSnapshotsBeforeWriterWait(t *testing.T) {
	t.Parallel()
	s, _ := headlessAccountingStore(t)
	blocker, err := s.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	waitCount := s.DB().Stats().WaitCount
	start := headlessAccountingStart("run")
	n := int64(7)
	attempts := []db.HeadlessAttemptRecord{{RunID: start.ID, AttemptNumber: 1, TokensIn: &n, FinalContent: "before"}}
	entries := []db.HeadlessAuxiliaryProviderAccounting{headlessAuxiliaryEntry(1, 1)}
	summary := db.HeadlessRunSummary{TokensOut: &n, FinalContent: "before"}
	done := make(chan error, 1)
	var wg sync.WaitGroup
	defer func() { _ = blocker.Rollback(); wg.Wait() }()
	wg.Go(func() { done <- commitHeadlessAuxiliary(t.Context(), s, start, attempts, entries, &summary) })
	// database/sql reports the queued BeginTx only after input snapshots exist.
	// This synchronization avoids sleeps and tests the actual blocking boundary.
	for s.DB().Stats().WaitCount == waitCount {
		select {
		case err := <-done:
			t.Fatalf("did not wait: %v", err)
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		default:
			runtime.Gosched()
		}
	}
	n = 99
	attempts[0].FinalContent = "after"
	entries[0].PayloadJSON[0] = '!'
	summary.FinalContent = "after"
	if err := blocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	a, err := s.ListHeadlessAttempts(t.Context(), start.ID)
	if err != nil || len(a) != 1 || a[0].FinalContent != "before" || *a[0].TokensIn != 7 {
		t.Fatalf("attempt alias: %+v %v", a, err)
	}
	r, err := s.GetHeadlessRun(t.Context(), start.ID)
	if err != nil || r.FinalContent != "before" || *r.TokensOut != 7 {
		t.Fatalf("summary alias: %+v %v", r, err)
	}
	if _, err := s.GetHeadlessAuxiliaryProviderAccounting(t.Context(), start.ID, "opaque-1-1"); err != nil {
		t.Fatal(err)
	}
}

func TestHeadlessAuxiliaryProviderAccountingQueuedCancellationAndBorrowedRollback(t *testing.T) {
	t.Parallel()
	s, _ := headlessAccountingStore(t)
	blocker, err := s.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	waitCount := s.DB().Stats().WaitCount
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	var wg sync.WaitGroup
	defer func() { cancel(); _ = blocker.Rollback(); wg.Wait() }()
	wg.Go(func() {
		done <- commitHeadlessAuxiliary(ctx, s, headlessAccountingStart("cancel-queued"), nil, []db.HeadlessAuxiliaryProviderAccounting{headlessAuxiliaryEntry(1, 1)}, nil)
	})
	for s.DB().Stats().WaitCount == waitCount {
		select {
		case err := <-done:
			t.Fatalf("did not queue: %v", err)
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		default:
			runtime.Gosched()
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("queued cancellation: %v", err)
	}
	if err := blocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetHeadlessRun(t.Context(), "cancel-queued"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("canceled start visible: %v", err)
	}
	rollback := errors.New("enclosing rollback")
	err = s.WithTx(t.Context(), func(tx *db.Store) error {
		if err := commitHeadlessAuxiliary(t.Context(), tx, headlessAccountingStart("borrowed"), nil, []db.HeadlessAuxiliaryProviderAccounting{headlessAuxiliaryEntry(1, 1)}, nil); err != nil {
			return err
		}
		if _, err := tx.GetHeadlessAuxiliaryProviderAccounting(t.Context(), "borrowed", "opaque-1-1"); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if _, err := s.GetHeadlessRun(t.Context(), "borrowed"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("enclosing rollback: %v", err)
	}
}

func TestHeadlessAuxiliaryProviderAccountingPaginationGaps(t *testing.T) {
	t.Parallel()
	s, _ := headlessAccountingStore(t)
	start := headlessAccountingStart("run")
	if err := commitHeadlessAuxiliary(t.Context(), s, start, nil, []db.HeadlessAuxiliaryProviderAccounting{headlessAuxiliaryEntry(1, 1)}, nil); err != nil {
		t.Fatal(err)
	}
	first, err := s.GetHeadlessAuxiliaryProviderAccounting(t.Context(), start.ID, "opaque-1-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := commitHeadlessAuxiliary(t.Context(), s, headlessAccountingStart("interleaved"), nil, []db.HeadlessAuxiliaryProviderAccounting{headlessAuxiliaryEntry(1, 1)}, nil); err != nil {
		t.Fatal(err)
	}
	if err := commitHeadlessAuxiliary(t.Context(), s, start, nil, []db.HeadlessAuxiliaryProviderAccounting{headlessAuxiliaryEntry(1, 2), headlessAuxiliaryEntry(1, 3)}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(t.Context(), "DELETE FROM headless_auxiliary_provider_accounting WHERE run_id=? AND invocation_id='opaque-1-2'", start.ID); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListHeadlessAuxiliaryProviderAccounting(t.Context(), start.ID, first.Sequence, 1)
	if err != nil || len(page) != 1 || page[0].Accounting.InvocationID != "opaque-1-3" || page[0].Sequence <= first.Sequence+1 {
		t.Fatalf("gap page: %+v %v", page, err)
	}
	if rest, err := s.ListHeadlessAuxiliaryProviderAccounting(t.Context(), start.ID, page[0].Sequence, 1); err != nil || len(rest) != 0 {
		t.Fatalf("tail: %+v %v", rest, err)
	}
}

func TestHeadlessAuxiliaryProviderAccountingExactChecksumAndReplayIntegrity(t *testing.T) {
	t.Parallel()
	s, _ := headlessAccountingStore(t)
	start := headlessAccountingStart("ab")
	entry := headlessAuxiliaryEntry(1, 1)
	entry.InvocationID = "c"
	entry.PayloadJSON = []byte(`{ "exact" : 1 }`)
	if err := commitHeadlessAuxiliary(t.Context(), s, start, nil, []db.HeadlessAuxiliaryProviderAccounting{entry}, nil); err != nil {
		t.Fatal(err)
	}
	var checksum string
	if err := s.DB().QueryRowContext(t.Context(), "SELECT checksum FROM headless_auxiliary_provider_accounting WHERE run_id=?", start.ID).Scan(&checksum); err != nil {
		t.Fatal(err)
	}
	var framed []byte
	for _, field := range [][]byte{[]byte(start.ID), []byte(entry.InvocationID), binary.BigEndian.AppendUint64(nil, 1), entry.PayloadJSON} {
		framed = binary.BigEndian.AppendUint64(framed, uint64(len(field)))
		framed = append(framed, field...)
	}
	digest := sha256.Sum256(framed)
	if checksum != hex.EncodeToString(digest[:]) {
		t.Fatal("checksum does not length-frame exact identity and payload")
	}
	changed := entry.Clone()
	changed.PayloadJSON = []byte(`{"exact":1}`)
	if err := commitHeadlessAuxiliary(t.Context(), s, start, nil, []db.HeadlessAuxiliaryProviderAccounting{changed}, nil); !errors.Is(err, db.ErrHeadlessAuxiliaryProviderAccountingConflict) {
		t.Fatal("normalized unequal bytes", err)
	}
	if _, err := s.DB().ExecContext(t.Context(), "UPDATE headless_auxiliary_provider_accounting SET checksum='corrupt'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetHeadlessAuxiliaryProviderAccounting(t.Context(), start.ID, entry.InvocationID); !errors.Is(err, db.ErrHeadlessAuxiliaryProviderAccountingCorrupt) {
		t.Fatal("get", err)
	}
	if err := commitHeadlessAuxiliary(t.Context(), s, start, nil, []db.HeadlessAuxiliaryProviderAccounting{entry}, nil); !errors.Is(err, db.ErrHeadlessAuxiliaryProviderAccountingCorrupt) {
		t.Fatal("replay accepted corruption", err)
	}
}
