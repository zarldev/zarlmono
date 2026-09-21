package engine_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/db"
)

func TestRecordedHistorySnapshotRetryAndAcknowledgment(t *testing.T) {
	store, err := db.Open(t.Context(), t.TempDir()+"/history.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveActiveSession(t.Context(), db.SessionRecord{ID: "session", Workspace: "/workspace"}); err != nil {
		t.Fatal(err)
	}
	ref, err := store.CaptureSessionHistory(t.Context(), "session", nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSessionCheckpoint(t.Context(), db.SessionCheckpoint{SessionID: "session", ID: "initial", SourceSessionID: "session", Workspace: "/workspace", BoundaryID: "prompt", FormatVersion: 2, Payload: []byte(`{}`), History: ref}); err != nil {
		t.Fatal(err)
	}
	provider := &requestRecordingProvider{}
	live := reservationRunner(t, provider)
	if err := live.RunRecordedTurn(t.Context(), "first", nil, store, "session"); err != nil {
		t.Fatal(err)
	}
	first := live.RecordedHistory("session")
	if len(first) != 3 {
		t.Fatalf("batches = %d, want prompt/request/answer", len(first))
	}
	model, err := store.GetSessionModelContext(t.Context(), "session")
	if err != nil {
		t.Fatal(err)
	}
	var captured struct {
		Request json.RawMessage `json:"request"`
	}
	if err := json.Unmarshal(model.RequestJSON, &captured); err != nil {
		t.Fatal(err)
	}
	actual, err := runner.MarshalHistoryRequest(provider.requests[0])
	if err != nil || !bytes.Equal(actual, captured.Request) {
		t.Fatalf("prepared request changed: %v", err)
	}
	// Last assistant is pending until full settlement, not confused with input.
	boundary, err := store.CaptureSessionHistory(t.Context(), "session", nil, []byte(`{}`))
	if err != nil || boundary.ReplayHead != model.HistoryHead {
		t.Fatalf("prepared input boundary: %v", err)
	}
	mutated := live.RecordedHistory("session")
	mutated[0].Messages[0][0] = '!'
	if bytes.Equal(mutated[0].Messages[0], live.RecordedHistory("session")[0].Messages[0]) {
		t.Fatal("retry snapshot aliases caller")
	}
	record, err := store.GetSession(t.Context(), "session")
	if err != nil {
		t.Fatal(err)
	}
	update := db.TranscriptUpdate{SessionID: "session", Workspace: "/workspace"}
	if err := store.CommitCompletedTurn(t.Context(), record, update, first...); err != nil {
		t.Fatal(err)
	}
	// An unacknowledged successful commit remains retryable with identical IDs.
	if err := store.CommitCompletedTurn(t.Context(), record, update, first...); err != nil {
		t.Fatal(err)
	}
	if err := live.RunRecordedTurn(t.Context(), "second", nil, store, "session"); err != nil {
		t.Fatal(err)
	}
	live.AcknowledgeRecordedHistory("session", first)
	live.AcknowledgeRecordedHistory("session", first)
	pending := live.RecordedHistory("session")
	if len(pending) != 3 {
		t.Fatalf("acknowledgment consumed later suffix: %d", len(pending))
	}
	if err := store.CommitCompletedTurn(t.Context(), record, update, pending...); err != nil {
		t.Fatal(err)
	}
	live.AcknowledgeRecordedHistory("session", pending)
	if len(live.RecordedHistory("session")) != 0 {
		t.Fatal("committed batches retained")
	}
	boundary, err = store.CaptureSessionHistory(t.Context(), "session", nil, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_, messages, _, err := store.ReadCheckpointHistory(t.Context(), boundary)
	if err != nil || len(messages) != 4 {
		t.Fatalf("retry duplicated occurrences: %d, %v", len(messages), err)
	}
	model, err = store.GetSessionModelContext(t.Context(), "session")
	if err != nil || model.Generation != 2 {
		t.Fatalf("request retry duplicated generation: %d, %v", model.Generation, err)
	}
	if len(provider.requests) != 2 {
		t.Fatal("history read/save regenerated responses")
	}
}
