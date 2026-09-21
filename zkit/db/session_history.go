package db

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/zarldev/zarlmono/zkit/db/gen"
)

const replayHistoryKind = "replay"

// CheckpointHistory identifies immutable transcript and replay prefixes and the
// separately stored application boundary metadata. Empty prefixes are valid.
type CheckpointHistory struct {
	TranscriptHead string
	ReplayHead     string
	StateID        string
	// PinID protects a public two-step capture until checkpoint publication or
	// ReleaseCapturedHistory. It is not part of immutable checkpoint identity.
	PinID string
}

// ModelContext is the latest prepared model request, not terminal working
// context. HistoryHead identifies its canonical input boundary; Generation
// increases on every request, including retries. RequestJSON owns its bytes.
type ModelContext struct {
	HistoryHead string
	Generation  uint64
	RequestJSON []byte
}

func historyDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *Store) putHistoryValue(ctx context.Context, data []byte) (string, error) {
	id := historyDigest(data)
	if err := s.q.InsertHistoryValue(ctx, gen.InsertHistoryValueParams{ID: id, Payload: data}); err != nil {
		return "", fmt.Errorf("save history value: %w", err)
	}
	return id, nil
}

func (s *Store) historyValue(ctx context.Context, id string) ([]byte, error) {
	data, err := s.q.GetHistoryValue(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCheckpointUnavailable
	}
	if err != nil {
		return nil, fmt.Errorf("read history value: %w", err)
	}
	if historyDigest(data) != id {
		return nil, ErrCheckpointCorrupt
	}
	return data, nil
}

func (s *Store) appendHistory(ctx context.Context, head string, values [][]byte) (string, error) {
	for _, data := range values {
		value, err := s.putHistoryValue(ctx, data)
		if err != nil {
			return "", err
		}
		id := historyDigest([]byte(head + ":" + value))
		if err := s.q.InsertHistoryNode(ctx, gen.InsertHistoryNodeParams{ID: id, ParentID: head, ValueID: value}); err != nil {
			return "", fmt.Errorf("append history: %w", err)
		}
		head = id
	}
	return head, nil
}

func (s *Store) readHistory(ctx context.Context, head string) ([][]byte, error) {
	var values [][]byte
	seen := make(map[string]bool)
	for head != "" {
		if seen[head] {
			return nil, ErrCheckpointCorrupt
		}
		seen[head] = true
		node, err := s.q.GetHistoryNode(ctx, head)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrCheckpointUnavailable
		}
		if err != nil {
			return nil, fmt.Errorf("read history node: %w", err)
		}
		if historyDigest([]byte(node.ParentID+":"+node.ValueID)) != head {
			return nil, ErrCheckpointCorrupt
		}
		value, err := s.historyValue(ctx, node.ValueID)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
		head = node.ParentID
	}
	slices.Reverse(values)
	return values, nil
}

func (s *Store) transcriptHistory(ctx context.Context, entries []TranscriptEntry) (string, error) {
	values := make([][]byte, len(entries))
	for i, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			return "", fmt.Errorf("encode history entry: %w", err)
		}
		values[i] = data
	}
	return s.appendHistory(ctx, "", values)
}

// CaptureSessionHistory stores immutable transcript versions and boundary
// metadata. It never imports replay data from display text or working context.
// Nonempty historical sessions without recorded replay remain unavailable.
// The returned capture owns a durable pin. Publish it with SaveSessionCheckpoint
// or explicitly ReleaseCapturedHistory when abandoning it; pins never expire.
func (s *Store) CaptureSessionHistory(ctx context.Context, sessionID string, entries []TranscriptEntry, state []byte) (CheckpointHistory, error) {
	var ref CheckpointHistory
	err := s.WithTx(ctx, func(tx *Store) error {
		if err := tx.q.AcquireCheckpointWrite(ctx); err != nil {
			return fmt.Errorf("acquire capture write: %w", err)
		}
		var err error
		ref, err = tx.captureSessionHistory(ctx, sessionID, entries, state)
		if err != nil {
			return err
		}
		ref.PinID = rand.Text()
		if err := tx.q.InsertHistoryPin(ctx, gen.InsertHistoryPinParams{ID: ref.PinID,
			TranscriptHead: ref.TranscriptHead, ReplayHead: ref.ReplayHead, StateID: ref.StateID}); err != nil {
			return fmt.Errorf("pin captured history: %w", err)
		}
		return nil
	})
	if err != nil {
		return CheckpointHistory{}, err
	}
	return ref, nil
}

func (s *Store) captureSessionHistory(ctx context.Context, sessionID string, entries []TranscriptEntry, state []byte) (CheckpointHistory, error) {
	var ref CheckpointHistory
	replay, err := s.q.GetHistoryHead(ctx, gen.GetHistoryHeadParams{SessionID: sessionID, Kind: replayHistoryKind})
	if errors.Is(err, sql.ErrNoRows) && len(entries) == 0 {
		err = nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ref, ErrCheckpointUnavailable
	}
	if err != nil {
		return ref, fmt.Errorf("read replay boundary: %w", err)
	}
	ref.ReplayHead = replay
	ref.TranscriptHead, err = s.transcriptHistory(ctx, entries)
	if err != nil {
		return ref, err
	}
	ref.StateID, err = s.putHistoryValue(ctx, state)
	return ref, err
}

// ReadCheckpointHistory resolves integrity-checked immutable values. It does
// not invoke tools, repair model messages, or substitute current session data.
func (s *Store) ReadCheckpointHistory(ctx context.Context, ref CheckpointHistory) ([]TranscriptEntry, [][]byte, []byte, error) {
	var entries []TranscriptEntry
	var replay [][]byte
	var state []byte
	err := s.withReadTx(ctx, func(tx *Store) error {
		values, err := tx.readHistory(ctx, ref.TranscriptHead)
		if err != nil {
			return err
		}
		entries = make([]TranscriptEntry, len(values))
		for i, value := range values {
			if json.Unmarshal(value, &entries[i]) != nil {
				return ErrCheckpointCorrupt
			}
		}
		replay, err = tx.readHistory(ctx, ref.ReplayHead)
		if err != nil {
			return err
		}
		state, err = tx.historyValue(ctx, ref.StateID)
		return err
	})
	return entries, replay, state, err
}

// AppendSessionReplay appends full canonical message occurrences atomically.
// JSON bytes are opaque to storage; the runner owns their message semantics.
// Identical consecutive messages remain distinct occurrences in the chain.
func (s *Store) AppendSessionReplay(ctx context.Context, sessionID string, messages [][]byte) error {
	return s.WithTx(ctx, func(tx *Store) error {
		return tx.appendSessionReplay(ctx, sessionID, messages)
	})
}

// SessionHistoryBatch is an ordered, independently owned replay append and optional
// prepared request. ID is stable across retries; RequestJSON is bound to the head
// after Messages. Reusing an ID with different content conflicts.
type SessionHistoryBatch struct {
	ID          string   `json:"id"`
	Messages    [][]byte `json:"messages,omitempty"`
	RequestJSON []byte   `json:"request,omitempty"`
}

// Clone returns a batch with independent serialized message and request bytes.
func (b SessionHistoryBatch) Clone() SessionHistoryBatch {
	b.RequestJSON = slices.Clone(b.RequestJSON)
	b.Messages = slices.Clone(b.Messages)
	for i := range b.Messages {
		b.Messages[i] = slices.Clone(b.Messages[i])
	}
	return b
}

// AppendSessionReplayBatch permits safe retry after an ambiguous commit.
func (s *Store) AppendSessionReplayBatch(ctx context.Context, sessionID, batchID string, messages [][]byte) error {
	return s.SaveSessionHistory(ctx, sessionID, []SessionHistoryBatch{{ID: batchID, Messages: messages}})
}

// SaveSessionHistory atomically records ordered idempotent batches. Prepared
// requests and their canonical input boundaries commit together before dispatch.
func (s *Store) SaveSessionHistory(ctx context.Context, sessionID string, batches []SessionHistoryBatch) error {
	return s.WithTx(ctx, func(tx *Store) error {
		return tx.saveSessionHistory(ctx, sessionID, batches)
	})
}

func (s *Store) saveSessionHistory(ctx context.Context, sessionID string, batches []SessionHistoryBatch) error {
	if len(batches) == 0 {
		return nil
	}
	// Acquire the writer before reading receipts/heads, including across Stores.
	if err := s.q.AcquireCheckpointWrite(ctx); err != nil {
		return fmt.Errorf("acquire history write: %w", err)
	}
	for _, batch := range batches {
		if batch.ID == "" {
			return ErrCheckpointCorrupt
		}
		data, err := json.Marshal(batch)
		if err != nil {
			return fmt.Errorf("encode replay batch: %w", err)
		}
		checksum := historyDigest(data)
		previous, err := s.q.GetHistoryBatch(ctx, gen.GetHistoryBatchParams{SessionID: sessionID, BatchID: batch.ID})
		if err == nil {
			if previous != checksum {
				return ErrCheckpointConflict
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read replay receipt: %w", err)
		}
		if err := s.appendSessionReplay(ctx, sessionID, batch.Messages); err != nil {
			return err
		}
		if len(batch.RequestJSON) != 0 {
			if err := s.saveSessionModelContext(ctx, sessionID, batch.RequestJSON); err != nil {
				return err
			}
		}
		if err := s.q.InsertHistoryBatch(ctx, gen.InsertHistoryBatchParams{SessionID: sessionID, BatchID: batch.ID, Checksum: checksum}); err != nil {
			return fmt.Errorf("save replay receipt: %w", err)
		}
	}
	return nil
}

func (s *Store) appendSessionReplay(ctx context.Context, sessionID string, messages [][]byte) error {
	for _, message := range messages {
		if !json.Valid(message) {
			return ErrCheckpointCorrupt
		}
	}
	head, err := s.q.GetHistoryHead(ctx, gen.GetHistoryHeadParams{SessionID: sessionID, Kind: replayHistoryKind})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrCheckpointUnavailable
	}
	if err != nil {
		return fmt.Errorf("read replay head: %w", err)
	}
	head, err = s.appendHistory(ctx, head, messages)
	if err != nil {
		return err
	}
	return s.q.SetHistoryHead(ctx, gen.SetHistoryHeadParams{SessionID: sessionID, Kind: replayHistoryKind, Head: head})
}

// SaveSessionModelContext replaces only the latest prepared request, bound to
// the current immutable replay head. It does not archive full context per turn.
func (s *Store) SaveSessionModelContext(ctx context.Context, sessionID string, request []byte) error {
	return s.WithTx(ctx, func(tx *Store) error { return tx.saveSessionModelContext(ctx, sessionID, request) })
}

func (s *Store) saveSessionModelContext(ctx context.Context, sessionID string, request []byte) error {
	if !json.Valid(request) {
		return ErrCheckpointCorrupt
	}
	head, err := s.q.GetHistoryHead(ctx, gen.GetHistoryHeadParams{SessionID: sessionID, Kind: replayHistoryKind})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrCheckpointUnavailable
	}
	if err != nil {
		return fmt.Errorf("read request history boundary: %w", err)
	}
	if err := s.q.SaveModelContext(ctx, gen.SaveModelContextParams{SessionID: sessionID, HistoryHead: head, RequestJson: request}); err != nil {
		return fmt.Errorf("save model context: %w", err)
	}
	return nil
}

// GetSessionModelContext returns the last prepared request independently of
// terminal working context, including its historical boundary and generation.
func (s *Store) GetSessionModelContext(ctx context.Context, sessionID string) (ModelContext, error) {
	row, err := s.read.GetModelContext(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return ModelContext{}, ErrNotFound
	}
	if err != nil {
		return ModelContext{}, fmt.Errorf("read model context: %w", err)
	}
	if row.Generation <= 0 || !json.Valid(row.RequestJson) {
		return ModelContext{}, ErrCheckpointCorrupt
	}
	return ModelContext{HistoryHead: row.HistoryHead, Generation: uint64(row.Generation), RequestJSON: row.RequestJson}, nil
}

func (s *Store) saveCheckpointHistory(ctx context.Context, checkpoint SessionCheckpoint) error {
	if checkpoint.History == (CheckpointHistory{}) {
		return nil
	}
	ref := checkpoint.History
	if _, err := s.historyValue(ctx, ref.StateID); err != nil {
		return err
	}
	// Detached legacy/released references may have lost their roots to explicit
	// GC. Never publish a checkpoint whose reachable chains are unavailable.
	mark := historyReachability{nodes: make(map[string]bool), values: make(map[string]bool)}
	for _, head := range []string{ref.TranscriptHead, ref.ReplayHead} {
		if err := mark.chain(ctx, s, head); err != nil {
			return err
		}
	}
	if err := s.q.InsertCheckpointHistory(ctx, gen.InsertCheckpointHistoryParams{
		SessionID: checkpoint.SessionID, CheckpointID: checkpoint.ID,
		TranscriptHead: ref.TranscriptHead, ReplayHead: ref.ReplayHead, StateID: ref.StateID,
	}); err != nil {
		return fmt.Errorf("save checkpoint history: %w", err)
	}
	for kind, head := range map[string]string{"transcript": ref.TranscriptHead, "replay": ref.ReplayHead} {
		// Only initial sessions and freshly-created branches lack a head. Saving an
		// older checkpoint must never rewind the owning session's current history.
		_, err := s.q.GetHistoryHead(ctx, gen.GetHistoryHeadParams{SessionID: checkpoint.SessionID, Kind: kind})
		if errors.Is(err, sql.ErrNoRows) {
			if err := s.q.SetHistoryHead(ctx, gen.SetHistoryHeadParams{SessionID: checkpoint.SessionID, Kind: kind, Head: head}); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	return s.ReleaseCapturedHistory(ctx, ref)
}
