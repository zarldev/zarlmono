package db_test

import (
	"bytes"
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/db"
)

func childEntry(id string, attempt int) db.HeadlessChildProviderAccounting {
	return db.HeadlessChildProviderAccounting{ChildTaskID: id, Attempt: attempt, Version: 1, PayloadJSON: []byte(`{"child":true}`)}
}

func commitChildren(ctx context.Context, s *db.Store, start db.HeadlessRunStart, children ...db.HeadlessChildProviderAccounting) error {
	return s.CommitHeadlessRecordingWithAccountingBatch(ctx, start, nil, db.HeadlessAccountingBatch{Children: children}, nil)
}

func TestHeadlessChildAccountingIdentityPagesCloneAndCascade(t *testing.T) {
	s, path := headlessAccountingStore(t)
	start := headlessAccountingStart("run ☃")
	first := childEntry("opaque-A", 1)
	clone := first.Clone()
	clone.PayloadJSON[0] = '!'
	if !bytes.Equal(first.PayloadJSON, []byte(`{"child":true}`)) {
		t.Fatal("clone aliases")
	}
	original := first.Clone()
	if err := commitChildren(t.Context(), s, start, first, first, childEntry("opaque-A", 2), childEntry("opaque-B", 1)); err != nil {
		t.Fatal(err)
	}
	first.PayloadJSON[0] = '!'
	row, err := s.GetHeadlessChildProviderAccounting(t.Context(), start.ID, "opaque-A", 1)
	if err != nil || row.Sequence == 0 || !reflect.DeepEqual(row.Accounting, original) {
		t.Fatalf("get: %+v %v", row, err)
	}
	row.Accounting.PayloadJSON[0] = '!'
	if err := commitChildren(t.Context(), s, start, original); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListHeadlessChildProviderAccounting(t.Context(), start.ID, 0, 1)
	if err != nil || len(page) != 1 || page[0].Sequence != row.Sequence {
		t.Fatalf("page: %+v %v", page, err)
	}
	rest, err := s.ListHeadlessChildProviderAccounting(t.Context(), start.ID, row.Sequence, 256)
	if err != nil || len(rest) != 2 || rest[0].Sequence <= row.Sequence || rest[1].Sequence <= rest[0].Sequence {
		t.Fatalf("rest: %+v %v", rest, err)
	}
	if err := commitChildren(t.Context(), s, headlessAccountingStart("other"), original); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetHeadlessChildProviderAccounting(t.Context(), "missing", "opaque-A", 1); !errors.Is(err, db.ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	again, err := reopened.GetHeadlessChildProviderAccounting(t.Context(), start.ID, "opaque-A", 1)
	if err != nil || again.Sequence != row.Sequence || !reflect.DeepEqual(again.Accounting, original) {
		t.Fatalf("reopen: %+v %v", again, err)
	}
	if _, err := reopened.DB().ExecContext(t.Context(), "DELETE FROM headless_runs WHERE id = ?", start.ID); err != nil {
		t.Fatal(err)
	}
	after, err := reopened.ListHeadlessChildProviderAccounting(t.Context(), start.ID, 0, 256)
	if err != nil || len(after) != 0 {
		t.Fatalf("cascade: %+v %v", after, err)
	}
}

func TestHeadlessChildAccountingConflictsBoundsAndCorruption(t *testing.T) {
	s, _ := headlessAccountingStore(t)
	start := headlessAccountingStart("child-corrupt")
	first := childEntry("a", 1)
	if err := commitChildren(t.Context(), s, start, first); err != nil {
		t.Fatal(err)
	}
	conflict := first.Clone()
	conflict.PayloadJSON = []byte(`{"changed":true}`)
	if err := commitChildren(t.Context(), s, start, childEntry("b", 1), conflict); !errors.Is(err, db.ErrHeadlessChildProviderAccountingConflict) {
		t.Fatalf("conflict: %v", err)
	}
	if _, err := s.GetHeadlessChildProviderAccounting(t.Context(), start.ID, "b", 1); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("partial child: %v", err)
	}
	for _, invalid := range []db.HeadlessChildProviderAccounting{{ChildTaskID: "a", Attempt: 1, Version: 2, PayloadJSON: []byte(`{}`)}, {ChildTaskID: "", Attempt: 1, Version: 1, PayloadJSON: []byte(`{}`)}, {ChildTaskID: "a", Attempt: 0, Version: 1, PayloadJSON: []byte(`{}`)}, {ChildTaskID: "a", Attempt: 1, Version: 1, PayloadJSON: []byte(`bad`)}, {ChildTaskID: "a", Attempt: 1, Version: 1, PayloadJSON: bytes.Repeat([]byte("x"), 1<<20+1)}} {
		want := db.ErrHeadlessChildProviderAccountingInvalidRequest
		if invalid.Version != 1 {
			want = db.ErrHeadlessChildProviderAccountingVersion
		}
		if err := commitChildren(t.Context(), s, start, invalid); !errors.Is(err, want) {
			t.Fatalf("invalid %+v: %v", invalid, err)
		}
	}
	if _, err := s.GetHeadlessChildProviderAccounting(t.Context(), start.ID, "", 1); !errors.Is(err, db.ErrHeadlessChildProviderAccountingInvalidRequest) {
		t.Fatal(err)
	}
	for _, limit := range []int{0, 257} {
		if _, err := s.ListHeadlessChildProviderAccounting(t.Context(), start.ID, 0, limit); !errors.Is(err, db.ErrHeadlessChildProviderAccountingInvalidRequest) {
			t.Fatal(err)
		}
	}
	if _, err := s.ListHeadlessChildProviderAccounting(t.Context(), start.ID, math.MaxUint64, 1); !errors.Is(err, db.ErrHeadlessChildProviderAccountingInvalidRequest) {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(t.Context(), "UPDATE headless_child_provider_accounting SET payload_json = ? WHERE run_id = ?", []byte(`{"transplanted":true}`), start.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetHeadlessChildProviderAccounting(t.Context(), start.ID, "a", 1); !errors.Is(err, db.ErrHeadlessChildProviderAccountingCorrupt) {
		t.Fatal(err)
	}
	if _, err := s.ListHeadlessChildProviderAccounting(t.Context(), start.ID, 0, 256); !errors.Is(err, db.ErrHeadlessChildProviderAccountingCorrupt) {
		t.Fatal(err)
	}
	if err := commitChildren(t.Context(), s, start, first); !errors.Is(err, db.ErrHeadlessChildProviderAccountingCorrupt) {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(t.Context(), "UPDATE headless_child_provider_accounting SET version = 2 WHERE run_id = ?", start.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetHeadlessChildProviderAccounting(t.Context(), start.ID, "a", 1); !errors.Is(err, db.ErrHeadlessChildProviderAccountingVersion) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.ListHeadlessChildProviderAccounting(ctx, start.ID, 0, 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestHeadlessChildAccountingDeferredCommitRollback(t *testing.T) {
	s, _ := headlessAccountingStore(t)
	start := headlessAccountingStart("deferred")
	_, err := s.DB().ExecContext(t.Context(), `CREATE TABLE deferred_owner(id INTEGER PRIMARY KEY);
 CREATE TABLE deferred_child(owner INTEGER REFERENCES deferred_owner(id) DEFERRABLE INITIALLY DEFERRED);
 CREATE TRIGGER reject_child AFTER INSERT ON headless_child_provider_accounting BEGIN INSERT INTO deferred_child VALUES (99); END;`)
	if err != nil {
		t.Fatal(err)
	}
	err = s.CommitHeadlessRecordingWithAccountingBatch(t.Context(), start, []db.HeadlessAttemptRecord{{RunID: start.ID, AttemptNumber: 1}}, db.HeadlessAccountingBatch{Providers: []db.HeadlessProviderAccounting{{PursueAttempt: 1, ProviderOrdinal: 1, Version: 1, PayloadJSON: []byte(`{}`)}}, Auxiliaries: []db.HeadlessAuxiliaryProviderAccounting{{InvocationID: "aux", Version: 1, PayloadJSON: []byte(`{}`)}}, Children: []db.HeadlessChildProviderAccounting{childEntry("child", 1)}}, &db.HeadlessRunSummary{EndedAt: time.Unix(2000, 0), FinalContent: "completed"})
	if err == nil {
		t.Fatal("expected deferred COMMIT failure")
	}
	if _, err := s.GetHeadlessRun(t.Context(), start.ID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("start survived: %v", err)
	}
	attempts, err := s.ListHeadlessAttempts(t.Context(), start.ID)
	if err != nil || len(attempts) != 0 {
		t.Fatalf("attempts: %+v %v", attempts, err)
	}
	for _, check := range []func() error{func() error { _, err := s.GetHeadlessProviderAccounting(t.Context(), start.ID, 1, 1); return err }, func() error {
		_, err := s.GetHeadlessAuxiliaryProviderAccounting(t.Context(), start.ID, "aux")
		return err
	}, func() error {
		_, err := s.GetHeadlessChildProviderAccounting(t.Context(), start.ID, "child", 1)
		return err
	}} {
		if err := check(); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("partial accounting: %v", err)
		}
	}
}
