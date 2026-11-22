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

func TestReadRecordedHeadlessProviderAccountingEnvelope(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		edit func(*engine.RecordedHeadlessProviderAccounting)
		want error
	}{
		{name: "valid"},
		{"run transplant", func(r *engine.RecordedHeadlessProviderAccounting) { r.RunID = "other" }, engine.ErrInvalidRecordedHeadlessProviderAccounting},
		{"pursue mismatch", func(r *engine.RecordedHeadlessProviderAccounting) { r.PursueAttempt++ }, engine.ErrInvalidRecordedHeadlessProviderAccounting},
		{"provider mismatch", func(r *engine.RecordedHeadlessProviderAccounting) { r.ProviderOrdinal++ }, engine.ErrInvalidRecordedHeadlessProviderAccounting},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "reader.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			r := recordedHeadlessAccountingFixture()
			run, a, o := r.RunID, r.PursueAttempt, r.ProviderOrdinal
			if tc.edit != nil {
				tc.edit(&r)
			}
			data := encodeRecordedHeadlessAccounting(t, r)
			if err := store.CommitHeadlessRecording(t.Context(), db.HeadlessRunStart{ID: run, StartedAt: time.Unix(1, 0)}, nil, []db.HeadlessProviderAccounting{{PursueAttempt: a, ProviderOrdinal: o, Version: 1, PayloadJSON: data}}, nil); err != nil {
				t.Fatal(err)
			}
			got, err := engine.ReadRecordedHeadlessProviderAccounting(t.Context(), store, run, a, o)
			if !errors.Is(err, tc.want) {
				t.Fatalf("read: %v want %v", err, tc.want)
			}
			if tc.want == nil && !bytes.Equal(data, encodeRecordedHeadlessAccounting(t, got)) {
				t.Fatal("reader changed evidence")
			}
			if tc.want != nil && got.RunID != "" {
				t.Fatal("reader returned partial evidence")
			}
			if _, err := engine.ReadRecordedHeadlessProviderAccounting(t.Context(), store, "absent", a, o); !errors.Is(err, db.ErrNotFound) {
				t.Fatalf("missing: %v", err)
			}
			if _, err := store.DB().ExecContext(t.Context(), "UPDATE headless_provider_accounting SET checksum='wrong'"); err != nil {
				t.Fatal(err)
			}
			if _, err := engine.ReadRecordedHeadlessProviderAccounting(t.Context(), store, run, a, o); !errors.Is(err, db.ErrHeadlessProviderAccountingCorrupt) {
				t.Fatalf("corrupt: %v", err)
			}
		})
	}
}

func TestRecordedHeadlessProviderAccountingLegacyWireUnchanged(t *testing.T) {
	t.Parallel()
	root, err := engine.EncodeRecordedProviderAccounting(engine.RecordedProviderAccounting{RequestBatchID: "request", TaskID: "task", Attempt: 1})
	if err != nil {
		t.Fatal(err)
	}
	const rootWant = `{"version":1,"request_batch_id":"request","task_id":"task","attempt":1,"provider":"","model":"","usage":null,"pricing_usage":null,"rate_card":null,"estimate_availability":"both_missing","configured_estimate":null,"reported_cost":null}`
	if string(root) != rootWant {
		t.Fatalf("root v1 changed: %s", root)
	}
	child, err := engine.EncodeRecordedChildProviderAccounting(engine.RecordedChildProviderAccounting{OwnerRequestBatchID: "request", ChildTaskID: "child", Attempt: 1})
	if err != nil {
		t.Fatal(err)
	}
	const childWant = `{"version":1,"owner_request_batch_id":"request","child_task_id":"child","attempt":1,"provider":"","model":"","usage":null,"pricing_usage":null,"rate_card":null,"estimate_availability":"both_missing","configured_estimate":null,"reported_cost":null}`
	if string(child) != childWant {
		t.Fatalf("child v1 changed: %s", child)
	}
	headless := encodeRecordedHeadlessAccounting(t, engine.RecordedHeadlessProviderAccounting{RunID: "run", PursueAttempt: 1, ProviderOrdinal: 1})
	const headlessWant = `{"version":1,"run_id":"run","pursue_attempt":1,"provider_ordinal":1,"provider":"","model":"","usage":null,"pricing_usage":null,"rate_card":null,"estimate_availability":"both_missing","configured_estimate":null,"reported_cost":null}`
	if string(headless) != headlessWant {
		t.Fatalf("headless v1: %s", headless)
	}
	if _, err := engine.DecodeRecordedHeadlessProviderAccounting(root); !errors.Is(err, engine.ErrInvalidRecordedHeadlessProviderAccounting) {
		t.Fatalf("root accepted as headless: %v", err)
	}
	if _, err := engine.DecodeRecordedHeadlessProviderAccounting(child); !errors.Is(err, engine.ErrInvalidRecordedHeadlessProviderAccounting) {
		t.Fatalf("child accepted as headless: %v", err)
	}
}
