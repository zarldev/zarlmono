package db_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/db"
)

func TestHeadlessChildAccountingDeferredCommitPreservesPriorState(t *testing.T) {
	s, _ := headlessAccountingStore(t)
	start := headlessAccountingStart("prior-deferred")
	old := childEntry("old", 1)
	if err := commitChildren(t.Context(), s, start, old); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetHeadlessRun(t.Context(), start.ID)
	if err != nil {
		t.Fatal(err)
	}
	original, err := s.GetHeadlessChildProviderAccounting(t.Context(), start.ID, "old", 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DB().ExecContext(t.Context(), `CREATE TABLE deferred_prior_owner(id INTEGER PRIMARY KEY);
 CREATE TABLE deferred_prior_child(owner INTEGER REFERENCES deferred_prior_owner(id) DEFERRABLE INITIALLY DEFERRED);
 CREATE TRIGGER reject_new_child AFTER INSERT ON headless_child_provider_accounting BEGIN INSERT INTO deferred_prior_child VALUES (99); END;`)
	if err != nil {
		t.Fatal(err)
	}
	err = s.CommitHeadlessRecordingWithAccountingBatch(t.Context(), start, []db.HeadlessAttemptRecord{{RunID: start.ID, AttemptNumber: 1, FinalContent: "new"}}, db.HeadlessAccountingBatch{
		Providers:   []db.HeadlessProviderAccounting{{PursueAttempt: 1, ProviderOrdinal: 1, Version: 1, PayloadJSON: []byte(`{}`)}},
		Auxiliaries: []db.HeadlessAuxiliaryProviderAccounting{{InvocationID: "aux", Version: 1, PayloadJSON: []byte(`{}`)}},
		Children:    []db.HeadlessChildProviderAccounting{old, childEntry("new", 1)},
	}, &db.HeadlessRunSummary{EndedAt: time.Unix(2000, 0), FinalContent: "new"})
	if err == nil {
		t.Fatal("expected deferred commit failure")
	}
	after, err := s.GetHeadlessRun(t.Context(), start.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("prior run: %+v %v", after, err)
	}
	again, err := s.GetHeadlessChildProviderAccounting(t.Context(), start.ID, "old", 1)
	if err != nil || !reflect.DeepEqual(original, again) {
		t.Fatalf("prior child: %+v %v", again, err)
	}
	attempts, err := s.ListHeadlessAttempts(t.Context(), start.ID)
	if err != nil || len(attempts) != 0 {
		t.Fatalf("attempts: %+v %v", attempts, err)
	}
	for _, check := range []func() error{func() error { _, err := s.GetHeadlessProviderAccounting(t.Context(), start.ID, 1, 1); return err }, func() error {
		_, err := s.GetHeadlessAuxiliaryProviderAccounting(t.Context(), start.ID, "aux")
		return err
	}, func() error {
		_, err := s.GetHeadlessChildProviderAccounting(t.Context(), start.ID, "new", 1)
		return err
	}} {
		if err := check(); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("partial receipt: %v", err)
		}
	}
}
