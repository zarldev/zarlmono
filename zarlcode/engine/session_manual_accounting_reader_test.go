package engine_test

import (
	"bytes"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestReadRecordedSessionManualAccounting(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*engine.RecordedSessionManualAccounting)
		wire func([]byte) []byte
		want error
	}{
		{name: "valid"},
		{name: "session mismatch", edit: func(r *engine.RecordedSessionManualAccounting) { r.SessionID = "other" }, want: engine.ErrInvalidRecordedSessionManualAccounting},
		{name: "invocation mismatch", edit: func(r *engine.RecordedSessionManualAccounting) {
			r.InvocationID = "abcdefab-1234-4234-9234-123456789abc"
		}, want: engine.ErrInvalidRecordedSessionManualAccounting},
		{name: "version", wire: func(b []byte) []byte { return bytes.Replace(b, []byte(`"version":1`), []byte(`"version":2`), 1) }, want: engine.ErrRecordedSessionManualAccountingVersion},
		{name: "bad payload", wire: func([]byte) []byte { return []byte(`{"PRIVATE-CANARY":1}`) }, want: engine.ErrInvalidRecordedSessionManualAccounting},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "reader.db")
			store, err := db.Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			if err := store.SaveSession(t.Context(), db.SessionRecord{ID: "session/opaque", Workspace: "/workspace"}); err != nil {
				t.Fatal(err)
			}
			record, err := store.GetSession(t.Context(), "session/opaque")
			if err != nil {
				t.Fatal(err)
			}
			r := manualFixture()
			if tc.edit != nil {
				tc.edit(&r)
			}
			wire := encodeManual(t, r)
			if tc.wire != nil {
				wire = tc.wire(wire)
			}
			batch := db.SessionAccountingBatch{Manual: []db.SessionManualProviderAccounting{{InvocationID: manualInvocation, Version: 1, PayloadJSON: wire}}}
			if err := store.CommitCompletedTurnWithAccountingBatch(t.Context(), record, db.TranscriptUpdate{SessionID: "session/opaque", Workspace: "/workspace"}, batch); err != nil {
				t.Fatal(err)
			}
			got, err := engine.ReadRecordedSessionManualAccounting(t.Context(), store, "session/opaque", manualInvocation)
			if !errors.Is(err, tc.want) {
				t.Fatalf("read: %v want %v", err, tc.want)
			}
			if tc.want == nil && !bytes.Equal(encodeManual(t, got), wire) {
				t.Fatal("read altered frozen bytes")
			}
			if tc.want != nil && (got.SessionID != "" || errors.Unwrap(err) != nil) {
				t.Fatal("partial record or unsafe codec error")
			}
			if _, err := engine.ReadRecordedSessionManualAccounting(t.Context(), store, "absent", manualInvocation); !errors.Is(err, db.ErrNotFound) {
				t.Fatalf("missing: %v", err)
			}
			if _, err := store.DB().ExecContext(t.Context(), "UPDATE session_manual_provider_accounting SET checksum='wrong'"); err != nil {
				t.Fatal(err)
			}
			if _, err := engine.ReadRecordedSessionManualAccounting(t.Context(), store, "session/opaque", manualInvocation); !errors.Is(err, db.ErrManualProviderAccountingCorrupt) {
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
			if _, err := engine.ReadRecordedSessionManualAccounting(t.Context(), reopened, "session/opaque", manualInvocation); !errors.Is(err, db.ErrManualProviderAccountingCorrupt) {
				t.Fatalf("reopen corruption: %v", err)
			}
			if tc.want == nil && !reflect.DeepEqual(got, decodeManual(t, wire)) {
				t.Fatal("reader ownership")
			}
		})
	}
}
