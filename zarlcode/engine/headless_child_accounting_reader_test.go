package engine_test

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestReadRecordedHeadlessChildProviderAccountingEnvelope(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		edit func(*engine.RecordedHeadlessChildProviderAccounting)
		want error
	}{
		{name: "valid"},
		{name: "run mismatch", edit: func(r *engine.RecordedHeadlessChildProviderAccounting) { r.RunID = "different" }, want: engine.ErrInvalidRecordedHeadlessChildProviderAccounting},
		{name: "task mismatch", edit: func(r *engine.RecordedHeadlessChildProviderAccounting) {
			r.ChildTaskID = "2a44e8fa-c36c-43c4-98c4-74e496acc637"
		}, want: engine.ErrInvalidRecordedHeadlessChildProviderAccounting},
		{name: "attempt mismatch", edit: func(r *engine.RecordedHeadlessChildProviderAccounting) { r.Attempt++ }, want: engine.ErrInvalidRecordedHeadlessChildProviderAccounting},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "child.db")
			store, err := db.Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			record := headlessChildFixture()
			run, task, attempt := record.RunID, record.ChildTaskID, record.Attempt
			if tc.edit != nil {
				tc.edit(&record)
			}
			payload := encodeHeadlessChild(t, record)
			batch := db.HeadlessAccountingBatch{Children: []db.HeadlessChildProviderAccounting{{ChildTaskID: task, Attempt: attempt, Version: 1, PayloadJSON: payload}}}
			if err := store.CommitHeadlessRecordingWithAccountingBatch(t.Context(), db.HeadlessRunStart{ID: run, StartedAt: time.Unix(1, 0)}, nil, batch, nil); err != nil {
				t.Fatal(err)
			}
			got, err := engine.ReadRecordedHeadlessChildProviderAccounting(t.Context(), store, run, task, attempt)
			if !errors.Is(err, tc.want) {
				t.Fatalf("read: %v want %v", err, tc.want)
			}
			if tc.want == nil && !bytes.Equal(encodeHeadlessChild(t, got), payload) {
				t.Fatal("read repriced or altered evidence")
			}
			if tc.want != nil && got.RunID != "" {
				t.Fatal("partial evidence")
			}
			if _, err := engine.ReadRecordedHeadlessChildProviderAccounting(t.Context(), store, "absent", task, attempt); !errors.Is(err, db.ErrNotFound) {
				t.Fatalf("missing: %v", err)
			}
			if tc.want == nil {
				got.RateCard.CacheWrites[0].Lifetime++
				again, err := engine.ReadRecordedHeadlessChildProviderAccounting(t.Context(), store, run, task, attempt)
				if err != nil || !bytes.Equal(encodeHeadlessChild(t, again), payload) {
					t.Fatalf("reader returned shared mutable evidence: %v", err)
				}
			}
			other, err := db.Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = other.Close() })
			if tc.want == nil {
				fromDisk, err := engine.ReadRecordedHeadlessChildProviderAccounting(t.Context(), other, run, task, attempt)
				if err != nil || !bytes.Equal(encodeHeadlessChild(t, fromDisk), payload) {
					t.Fatalf("reopen changed evidence: %v", err)
				}
			}
			if err := other.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := store.DB().ExecContext(t.Context(), "UPDATE headless_child_provider_accounting SET checksum='wrong'"); err != nil {
				t.Fatal(err)
			}
			if _, err := engine.ReadRecordedHeadlessChildProviderAccounting(t.Context(), store, run, task, attempt); !errors.Is(err, db.ErrHeadlessChildProviderAccountingCorrupt) {
				t.Fatalf("corrupt: %v", err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := db.Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			if _, err := engine.ReadRecordedHeadlessChildProviderAccounting(t.Context(), reopened, run, task, attempt); !errors.Is(err, db.ErrHeadlessChildProviderAccountingCorrupt) {
				t.Fatalf("reopened: %v", err)
			}
		})
	}
}
