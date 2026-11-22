package db_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/db"
)

func TestHeadlessAuxiliaryBatchIndependentStoresAtomicWinner(t *testing.T) {
	t.Parallel()
	s, path := headlessAccountingStore(t)
	other, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	for _, same := range []bool{true, false} {
		start := headlessAccountingStart(map[bool]string{true: "same-batch", false: "conflicting-batch"}[same])
		a := db.HeadlessAccountingBatch{Providers: []db.HeadlessProviderAccounting{headlessAccountingEntry(1, 1)}, Auxiliaries: []db.HeadlessAuxiliaryProviderAccounting{headlessAuxiliaryEntry(1, 1)}}
		b := db.HeadlessAccountingBatch{Providers: []db.HeadlessProviderAccounting{a.Providers[0].Clone()}, Auxiliaries: []db.HeadlessAuxiliaryProviderAccounting{a.Auxiliaries[0].Clone()}}
		if !same {
			b.Auxiliaries[0].PayloadJSON = []byte(`{"winner":"second"}`)
		}
		ready := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		wg.Go(func() {
			<-ready
			results <- s.CommitHeadlessRecordingWithAccountingBatch(t.Context(), start, []db.HeadlessAttemptRecord{{RunID: start.ID, AttemptNumber: 1, FinalContent: "first"}}, a, &db.HeadlessRunSummary{FinalContent: "first"})
		})
		wg.Go(func() {
			<-ready
			results <- other.CommitHeadlessRecordingWithAccountingBatch(t.Context(), start, []db.HeadlessAttemptRecord{{RunID: start.ID, AttemptNumber: 1, FinalContent: "second"}}, b, &db.HeadlessRunSummary{FinalContent: "second"})
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
		if same && (successes != 2 || conflicts != 0) || !same && (successes != 1 || conflicts != 1) {
			t.Fatalf("success=%d conflict=%d", successes, conflicts)
		}
		run, err := s.GetHeadlessRun(t.Context(), start.ID)
		if err != nil {
			t.Fatal(err)
		}
		attempts, err := s.ListHeadlessAttempts(t.Context(), start.ID)
		if err != nil || len(attempts) != 1 || attempts[0].FinalContent != run.FinalContent {
			t.Fatal("partial winning summary", err)
		}
		aux, err := s.GetHeadlessAuxiliaryProviderAccounting(t.Context(), start.ID, a.Auxiliaries[0].InvocationID)
		if err != nil {
			t.Fatal(err)
		}
		want := a.Auxiliaries[0].PayloadJSON
		if !same && run.FinalContent == "second" {
			want = b.Auxiliaries[0].PayloadJSON
		}
		if !bytes.Equal(aux.Accounting.PayloadJSON, want) {
			t.Fatal("losing transaction changed attempt/completion")
		}
		if _, err := s.GetHeadlessProviderAccounting(t.Context(), start.ID, 1, 1); err != nil {
			t.Fatal("main missing", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.CommitHeadlessRecordingWithAccountingBatch(ctx, headlessAccountingStart("cancel-batch"), nil, db.HeadlessAccountingBatch{Providers: []db.HeadlessProviderAccounting{headlessAccountingEntry(1, 1)}, Auxiliaries: []db.HeadlessAuxiliaryProviderAccounting{headlessAuxiliaryEntry(1, 1)}}, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel batch", err)
	}
	if _, err := s.GetHeadlessRun(t.Context(), "cancel-batch"); !errors.Is(err, db.ErrNotFound) {
		t.Fatal("cancel visible", err)
	}
}

func headlessBatchSnapshot(t *testing.T, s *db.Store) map[string][][]any {
	t.Helper()
	out := make(map[string][][]any)
	for _, name := range []string{"headless_runs", "headless_attempts", "headless_provider_accounting", "headless_auxiliary_provider_accounting", "sqlite_sequence"} {
		out[name] = childMigrationRows(t, s.DB(), name)
	}
	return out
}

func TestHeadlessAuxiliaryBatchAllSetsRollbackRetry(t *testing.T) {
	t.Parallel()
	for _, prior := range []bool{false, true} {
		for _, failure := range []string{"start", "attempt", "main", "auxiliary", "completion", "late conflict", "commit"} {
			t.Run(failure+map[bool]string{true: " prior", false: " new"}[prior], func(t *testing.T) {
				t.Parallel()
				s, _ := headlessAccountingStore(t)
				start := headlessAccountingStart("batch")
				n := int64(9)
				attempt := db.HeadlessAttemptRecord{RunID: start.ID, AttemptNumber: 1, FinalContent: "new", TokensIn: &n, RecordedAt: time.Unix(1111, 0)}
				completion := db.HeadlessRunSummary{FinalContent: "new", TokensOut: &n, EndedAt: time.Unix(2222, 0)}
				batch := db.HeadlessAccountingBatch{Providers: []db.HeadlessProviderAccounting{headlessAccountingEntry(1, 1)}, Auxiliaries: []db.HeadlessAuxiliaryProviderAccounting{headlessAuxiliaryEntry(1, 1)}}
				if prior {
					old := attempt
					old.FinalContent = "old"
					if err := s.CommitHeadlessRecordingWithAccountingBatch(t.Context(), start, []db.HeadlessAttemptRecord{old}, batch, &db.HeadlessRunSummary{FinalContent: "old", EndedAt: time.Unix(2000, 0)}); err != nil {
						t.Fatal(err)
					}
					if err := s.UpdateHeadlessRunProgress(t.Context(), start.ID, 9, 7); err != nil {
						t.Fatal(err)
					}
					batch.Providers = append(batch.Providers, headlessAccountingEntry(1, 2))
					batch.Auxiliaries = append(batch.Auxiliaries, headlessAuxiliaryEntry(1, 2))
				}
				before := headlessBatchSnapshot(t, s)
				var setup string
				switch failure {
				case "start":
					setup = `CREATE TRIGGER reject_batch BEFORE INSERT ON headless_runs BEGIN SELECT RAISE(ABORT,'synthetic'); END;`
				case "attempt":
					setup = `CREATE TRIGGER reject_batch BEFORE INSERT ON headless_attempts BEGIN SELECT RAISE(ABORT,'synthetic'); END;`
				case "main":
					setup = `CREATE TRIGGER reject_batch BEFORE INSERT ON headless_provider_accounting BEGIN SELECT RAISE(ABORT,'synthetic'); END;`
				case "auxiliary":
					setup = `CREATE TRIGGER reject_batch BEFORE INSERT ON headless_auxiliary_provider_accounting BEGIN SELECT RAISE(ABORT,'synthetic'); END;`
				case "completion":
					setup = `CREATE TRIGGER reject_batch BEFORE UPDATE ON headless_runs BEGIN SELECT RAISE(ABORT,'synthetic'); END;`
				case "commit":
					setup = `CREATE TABLE deferred_batch_owner(id INTEGER PRIMARY KEY); CREATE TABLE deferred_batch_child(owner INTEGER REFERENCES deferred_batch_owner(id) DEFERRABLE INITIALLY DEFERRED); CREATE TRIGGER reject_batch AFTER INSERT ON headless_auxiliary_provider_accounting BEGIN INSERT INTO deferred_batch_child VALUES (99); END;`
				case "late conflict":
					changed := batch.Auxiliaries[0].Clone()
					changed.PayloadJSON = []byte(`{"changed":true}`)
					batch.Auxiliaries = append(batch.Auxiliaries, changed)
				}
				if setup != "" {
					if _, err := s.DB().ExecContext(t.Context(), setup); err != nil {
						t.Fatal(err)
					}
				}
				err := s.CommitHeadlessRecordingWithAccountingBatch(t.Context(), start, []db.HeadlessAttemptRecord{attempt}, batch, &completion)
				if err == nil {
					t.Fatal("accepted rejected batch")
				}
				if failure == "late conflict" && !errors.Is(err, db.ErrHeadlessAuxiliaryProviderAccountingConflict) {
					t.Fatal(err)
				}
				if after := headlessBatchSnapshot(t, s); !reflect.DeepEqual(before, after) {
					t.Fatalf("partial changes: before=%v after=%v", before, after)
				}
				if failure == "late conflict" {
					batch.Auxiliaries = batch.Auxiliaries[:len(batch.Auxiliaries)-1]
				} else {
					if _, err := s.DB().ExecContext(t.Context(), `DROP TRIGGER reject_batch`); err != nil {
						t.Fatal(err)
					}
				}
				start.StartedAt = time.Unix(start.StartedAt.Unix(), 999999999)
				if err := s.CommitHeadlessRecordingWithAccountingBatch(t.Context(), start, []db.HeadlessAttemptRecord{attempt}, batch, &completion); err != nil {
					t.Fatal("retry", err)
				}
				got, err := s.GetHeadlessRun(t.Context(), start.ID)
				if err != nil || got.FinalContent != "new" || got.TokensOut == nil || *got.TokensOut != 9 {
					t.Fatalf("completion %+v %v", got, err)
				}
				attempts, err := s.ListHeadlessAttempts(t.Context(), start.ID)
				if err != nil || len(attempts) != 1 || attempts[0].FinalContent != "new" {
					t.Fatalf("attempts %+v %v", attempts, err)
				}
				for _, entry := range batch.Providers {
					if _, err := s.GetHeadlessProviderAccounting(t.Context(), start.ID, entry.PursueAttempt, entry.ProviderOrdinal); err != nil {
						t.Fatal(err)
					}
				}
				for _, entry := range batch.Auxiliaries {
					if _, err := s.GetHeadlessAuxiliaryProviderAccounting(t.Context(), start.ID, entry.InvocationID); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestHeadlessAuxiliaryBatchSnapshotsAndBorrowedRollback(t *testing.T) {
	t.Parallel()
	s, _ := headlessAccountingStore(t)
	blocker, err := s.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	defer func() { _ = blocker.Rollback(); wg.Wait() }()
	wait := s.DB().Stats().WaitCount
	start := headlessAccountingStart("snapshot")
	n := int64(7)
	attempts := []db.HeadlessAttemptRecord{{RunID: start.ID, AttemptNumber: 1, TokensIn: &n}}
	batch := db.HeadlessAccountingBatch{Providers: []db.HeadlessProviderAccounting{headlessAccountingEntry(1, 1)}, Auxiliaries: []db.HeadlessAuxiliaryProviderAccounting{headlessAuxiliaryEntry(1, 1)}}
	mainWant, auxWant := batch.Providers[0].Clone(), batch.Auxiliaries[0].Clone()
	summary := db.HeadlessRunSummary{TokensOut: &n}
	done := make(chan error, 1)
	wg.Go(func() {
		done <- s.CommitHeadlessRecordingWithAccountingBatch(t.Context(), start, attempts, batch, &summary)
	})
	for s.DB().Stats().WaitCount == wait {
		select {
		case err := <-done:
			t.Fatalf("did not wait %v", err)
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		default:
			runtime.Gosched()
		}
	}
	n = 99
	attempts[0].AttemptNumber = 2
	batch.Providers[0].PayloadJSON[0] = '!'
	batch.Auxiliaries[0].PayloadJSON[0] = '!'
	batch.Auxiliaries[0].InvocationID = "mutated"
	summary.FinalContent = "mutated"
	if err := blocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	main, err := s.GetHeadlessProviderAccounting(t.Context(), start.ID, 1, 1)
	if err != nil || !bytes.Equal(main.Accounting.PayloadJSON, mainWant.PayloadJSON) {
		t.Fatal("main alias", err)
	}
	aux, err := s.GetHeadlessAuxiliaryProviderAccounting(t.Context(), start.ID, auxWant.InvocationID)
	if err != nil || !reflect.DeepEqual(aux.Accounting, auxWant) {
		t.Fatal("aux alias", err)
	}
	run, err := s.GetHeadlessRun(t.Context(), start.ID)
	if err != nil || run.TokensOut == nil || *run.TokensOut != 7 || run.FinalContent != "" {
		t.Fatal("summary alias", err)
	}
	records, err := s.ListHeadlessAttempts(t.Context(), start.ID)
	if err != nil || len(records) != 1 || records[0].AttemptNumber != 1 || *records[0].TokensIn != 7 {
		t.Fatal("attempt alias", err)
	}
	rollback := errors.New("owner rollback")
	before := headlessBatchSnapshot(t, s)
	err = s.WithTx(t.Context(), func(tx *db.Store) error {
		if err := tx.CommitHeadlessRecordingWithAccountingBatch(t.Context(), headlessAccountingStart("borrowed-batch"), nil, db.HeadlessAccountingBatch{Providers: []db.HeadlessProviderAccounting{mainWant}, Auxiliaries: []db.HeadlessAuxiliaryProviderAccounting{auxWant}}, &db.HeadlessRunSummary{}); err != nil {
			return err
		}
		if _, err := tx.GetHeadlessAuxiliaryProviderAccounting(t.Context(), "borrowed-batch", auxWant.InvocationID); err != nil {
			return err
		}
		if _, err := s.GetHeadlessRun(t.Context(), "borrowed-batch"); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("partial visibility %v", err)
		}
		return rollback
	})
	if !errors.Is(err, rollback) || !reflect.DeepEqual(before, headlessBatchSnapshot(t, s)) {
		t.Fatal("borrowed owner rollback", err)
	}
}

func TestHeadlessAuxiliaryBatchValidationPrecedence(t *testing.T) {
	s, _ := headlessAccountingStore(t)
	start := headlessAccountingStart("precedence")
	main, aux := headlessAccountingEntry(0, 1), headlessAuxiliaryEntry(1, 1)
	aux.Version = 2
	batch := db.HeadlessAccountingBatch{Providers: []db.HeadlessProviderAccounting{main}, Auxiliaries: []db.HeadlessAuxiliaryProviderAccounting{aux}}
	if err := s.CommitHeadlessRecordingWithAccountingBatch(t.Context(), start, nil, batch, nil); !errors.Is(err, db.ErrHeadlessProviderAccountingInvalidRequest) {
		t.Fatal(err)
	}
	batch.Providers[0].Version = 2
	if err := s.CommitHeadlessRecordingWithAccountingBatch(t.Context(), start, nil, batch, nil); !errors.Is(err, db.ErrHeadlessProviderAccountingVersion) {
		t.Fatal(err)
	}
	if err := s.CommitHeadlessRecordingWithAccountingBatch(t.Context(), start, []db.HeadlessAttemptRecord{{RunID: "other"}}, batch, nil); !errors.Is(err, db.ErrHeadlessProviderAccountingInvalidRequest) {
		t.Fatal(err)
	}
	batch.Providers = nil
	if err := s.CommitHeadlessRecordingWithAccountingBatch(t.Context(), start, nil, batch, nil); !errors.Is(err, db.ErrHeadlessAuxiliaryProviderAccountingVersion) {
		t.Fatal(err)
	}
}
