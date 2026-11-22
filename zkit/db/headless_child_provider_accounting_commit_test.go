package db_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zkit/db"
)

func TestHeadlessChildAccountingBorrowedTransactionAndPriorState(t *testing.T) {
	s, _ := headlessAccountingStore(t)
	start := headlessAccountingStart("prior")
	original := childEntry("existing", 1)
	if err := commitChildren(t.Context(), s, start, original); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetHeadlessRun(t.Context(), start.ID)
	if err != nil {
		t.Fatal(err)
	}
	changed := original.Clone()
	changed.PayloadJSON = []byte(`{"conflict":true}`)
	err = s.CommitHeadlessRecordingWithAccountingBatch(t.Context(), start, []db.HeadlessAttemptRecord{{RunID: start.ID, AttemptNumber: 1, FinalContent: "new"}}, db.HeadlessAccountingBatch{Children: []db.HeadlessChildProviderAccounting{childEntry("new", 1), changed}}, &db.HeadlessRunSummary{FinalContent: "new"})
	if !errors.Is(err, db.ErrHeadlessChildProviderAccountingConflict) {
		t.Fatalf("conflict: %v", err)
	}
	after, err := s.GetHeadlessRun(t.Context(), start.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("prior summary changed: %+v %v", after, err)
	}
	attempts, err := s.ListHeadlessAttempts(t.Context(), start.ID)
	if err != nil || len(attempts) != 0 {
		t.Fatalf("partial attempts: %+v %v", attempts, err)
	}
	if _, err := s.GetHeadlessChildProviderAccounting(t.Context(), start.ID, "new", 1); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("partial child: %v", err)
	}
	err = s.WithTx(t.Context(), func(tx *db.Store) error {
		if err := commitChildren(t.Context(), tx, start, childEntry("borrowed", 1)); err != nil {
			return err
		}
		return errors.New("owner rollback")
	})
	if err == nil {
		t.Fatal("expected owner rollback")
	}
	if _, err := s.GetHeadlessChildProviderAccounting(t.Context(), start.ID, "borrowed", 1); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("borrowed partial: %v", err)
	}
}

func TestHeadlessChildAccountingConcurrentStores(t *testing.T) {
	s, path := headlessAccountingStore(t)
	other, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	start := headlessAccountingStart("concurrent")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, store := range []*db.Store{s, other} {
		wg.Add(1)
		go func(store *db.Store) {
			defer wg.Done()
			errs <- commitChildren(t.Context(), store, start, childEntry("same", 1))
		}(store)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.ListHeadlessChildProviderAccounting(t.Context(), start.ID, 0, 256)
	if err != nil || len(rows) != 1 {
		t.Fatalf("concurrent receipts: %+v %v", rows, err)
	}
}
