package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
)

// sessionHistorySink borrows the store until the turn and persistence FIFO drain.
// Serialized batches stay owned by the runner until a full save acknowledges them.
type sessionHistorySink struct {
	store     *db.Store
	sessionID string
	buffer    *sessionHistoryBuffer
	provider  string
	model     string
}

var _ runner.HistorySink = sessionHistorySink{}

func (s sessionHistorySink) Append(_ context.Context, records []runner.ReplayMessage) error {
	values := make([][]byte, len(records))
	for i, record := range records {
		data, err := runner.MarshalReplayMessage(record)
		if err != nil {
			return fmt.Errorf("encode canonical occurrence: %w", err)
		}
		values[i] = data
	}
	s.buffer.mu.Lock()
	defer s.buffer.mu.Unlock()
	s.buffer.pending = append(s.buffer.pending, db.SessionHistoryBatch{ID: uuid.NewString(), Messages: values})
	return nil
}

func (s sessionHistorySink) Request(ctx context.Context, request llm.CompletionRequest) error {
	requestJSON, err := runner.MarshalHistoryRequest(request)
	if err != nil {
		return fmt.Errorf("encode prepared model request: %w", err)
	}
	data, err := json.Marshal(struct {
		Provider string          `json:"provider"`
		Model    string          `json:"model"`
		Request  json.RawMessage `json:"request"`
	}{Provider: s.provider, Model: s.model, Request: requestJSON})
	if err != nil {
		return fmt.Errorf("encode prepared model request: %w", err)
	}
	s.buffer.mu.Lock()
	defer s.buffer.mu.Unlock()
	s.buffer.pending = append(s.buffer.pending, db.SessionHistoryBatch{ID: uuid.NewString(), RequestJSON: data})
	// The provider must not run before its exact request and input boundary are
	// durable. Keep receipts/bytes through settlement, including ambiguous errors.
	return s.store.SaveSessionHistory(ctx, s.sessionID, s.buffer.pending)
}

// RunRecordedTurn dispatches a reserved turn with canonical replay capture and
// separately stored prepared model input. The caller first commits its BEFORE
// checkpoint and lends store until the turn and persistence FIFO have drained.
func (r *RuntimeReservation) RunRecordedTurn(ctx context.Context, prompt string, attachments []llm.ContentPart, store *db.Store, sessionID string) error {
	target := r.owner.RunTarget()
	return r.runTurn(ctx, prompt, attachments, runner.WithHistorySink(sessionHistorySink{
		store: store, sessionID: sessionID, provider: target.Spec.Name, model: target.Model, buffer: r.owner.historyBuffer(sessionID),
	}))
}

// RunRecordedTurn records a session-owned turn when no BEFORE checkpoint can be
// created. Admission and shutdown ownership are identical to RunTurnWithAttachments.
func (l *LiveRunner) RunRecordedTurn(ctx context.Context, prompt string, attachments []llm.ContentPart, store *db.Store, sessionID string) error {
	if !l.admission.enter() {
		return l.admission.rejection()
	}
	defer l.admission.leave()
	target := l.RunTarget()
	return l.runTurnAdmitted(ctx, runner.TaskSpec{Prompt: prompt, Attachments: attachments}, func() {}, runner.WithHistorySink(sessionHistorySink{
		store: store, sessionID: sessionID, provider: target.Spec.Name, model: target.Model, buffer: l.historyBuffer(sessionID),
	}))
}

// sessionHistoryBuffer owns serialized batches, never runner aliases. No writer
// goroutine exists: request capture and the application's save FIFO own writes.
type sessionHistoryBuffer struct {
	mu      sync.Mutex
	pending []db.SessionHistoryBatch
}

func (l *LiveRunner) historyBuffer(sessionID string) *sessionHistoryBuffer {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.historyBuffers == nil {
		l.historyBuffers = make(map[string]*sessionHistoryBuffer)
	}
	buffer, ok := l.historyBuffers[sessionID]
	if !ok {
		buffer = &sessionHistoryBuffer{}
		l.historyBuffers[sessionID] = buffer
	}
	return buffer
}

// RecordedHistory returns independently owned batches through the current
// boundary. A full-save snapshot retains these exact bytes for every retry.
func (l *LiveRunner) RecordedHistory(sessionID string) []db.SessionHistoryBatch {
	buffer := l.historyBuffer(sessionID)
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	batches := make([]db.SessionHistoryBatch, len(buffer.pending))
	for i, batch := range buffer.pending {
		batches[i] = batch.Clone()
	}
	return batches
}

// AcknowledgeRecordedHistory releases only batches included in a successful
// atomic full save. Later occurrences remain pending; repeated acknowledgments
// are harmless. Call only after CommitCompletedTurn/CommitCheckpointTurn succeeds.
func (l *LiveRunner) AcknowledgeRecordedHistory(sessionID string, batches []db.SessionHistoryBatch) {
	buffer := l.historyBuffer(sessionID)
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	ids := make(map[string]bool, len(batches))
	for _, batch := range batches {
		ids[batch.ID] = true
	}
	pending := buffer.pending[:0]
	for _, batch := range buffer.pending {
		if !ids[batch.ID] {
			pending = append(pending, batch)
		}
	}
	clear(buffer.pending[len(pending):])
	buffer.pending = pending
}
