package db

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zkit/db/gen"
)

// ErrProviderAccountingConflict means a request receipt already has different accounting.
var ErrProviderAccountingConflict = errors.New("session provider accounting conflict")

// ErrProviderAccountingCorrupt means stored accounting failed envelope integrity checks.
var ErrProviderAccountingCorrupt = errors.New("session provider accounting is corrupted")

// ErrProviderAccountingVersion means the accounting envelope version is unsupported.
var ErrProviderAccountingVersion = errors.New("unsupported session provider accounting version")

// ErrProviderAccountingInvalidRequest means accounting input, scope, or pagination is invalid,
// or its referenced request receipt does not exist in that session.
var ErrProviderAccountingInvalidRequest = errors.New("invalid session provider accounting request")

const maxProviderAccountingPayload = 1 << 20

// SessionProviderAccounting is an immutable settlement envelope for a recorded
// request receipt. Version 1 accepts valid UTF-8 JSON up to 1 MiB; application
// payload semantics belong to the caller, not the database. PayloadJSON owns its bytes.
type SessionProviderAccounting struct {
	RequestBatchID string
	Version        uint64
	PayloadJSON    []byte
}

// Clone returns an envelope with independent payload bytes.
func (r SessionProviderAccounting) Clone() SessionProviderAccounting {
	r.PayloadJSON = slices.Clone(r.PayloadJSON)
	return r
}

// SessionProviderAccountingRecord pairs a settlement with its opaque increasing
// pagination cursor. Sequence is not a contiguous count or a per-session ordinal.
type SessionProviderAccountingRecord struct {
	Sequence   uint64
	Accounting SessionProviderAccounting
}

// SaveSessionProviderAccounting atomically saves settlements for existing request
// receipts in sessionID. Identical version/bytes are idempotent and preserve the
// original cursor; changed content conflicts. A transaction-bound Store borrows
// its enclosing transaction. The caller must not mutate input during the call.
func (s *Store) SaveSessionProviderAccounting(ctx context.Context, sessionID string, accounting []SessionProviderAccounting) error {
	if err := validateProviderAccountingInput(sessionID, accounting); err != nil {
		return err
	}
	accounting = cloneProviderAccounting(accounting)
	if s.inTx {
		return s.saveSessionProviderAccounting(ctx, sessionID, accounting)
	}
	return s.WithTx(ctx, func(tx *Store) error {
		return tx.saveSessionProviderAccounting(ctx, sessionID, accounting)
	})
}

func validateProviderAccountingInput(sessionID string, accounting []SessionProviderAccounting) error {
	if sessionID == "" {
		return ErrProviderAccountingInvalidRequest
	}
	for _, entry := range accounting {
		if err := validateProviderAccounting(entry); err != nil {
			return err
		}
	}
	return nil
}

func cloneProviderAccounting(accounting []SessionProviderAccounting) []SessionProviderAccounting {
	owned := make([]SessionProviderAccounting, len(accounting))
	for i, entry := range accounting {
		owned[i] = entry.Clone()
	}
	return owned
}

// saveSessionProviderAccounting requires a transaction-bound Store.
func (s *Store) saveSessionProviderAccounting(ctx context.Context, sessionID string, accounting []SessionProviderAccounting) error {
	if len(accounting) == 0 {
		return nil
	}
	// Acquire SQLite's writer before reading receipts, including across Stores.
	if err := s.q.AcquireCheckpointWrite(ctx); err != nil {
		return fmt.Errorf("acquire accounting write: %w", err)
	}
	for _, entry := range accounting {
		previous, err := s.q.GetSessionProviderAccounting(ctx, gen.GetSessionProviderAccountingParams{
			SessionID: sessionID, RequestBatchID: entry.RequestBatchID,
		})
		if err == nil {
			record, err := providerAccountingRecord(gen.SessionProviderAccounting(previous))
			if err != nil {
				return err
			}
			if record.Accounting.Version != entry.Version || !bytes.Equal(record.Accounting.PayloadJSON, entry.PayloadJSON) {
				return ErrProviderAccountingConflict
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read accounting receipt: %w", err)
		}
		// This read is protected by the writer acquired above. The schema FK is
		// the final constraint; absence is an input error, not a driver diagnostic.
		if _, err := s.q.GetHistoryBatch(ctx, gen.GetHistoryBatchParams{SessionID: sessionID, BatchID: entry.RequestBatchID}); errors.Is(err, sql.ErrNoRows) {
			return ErrProviderAccountingInvalidRequest
		} else if err != nil {
			return fmt.Errorf("read accounting request receipt: %w", err)
		}
		if err := s.q.InsertSessionProviderAccounting(ctx, gen.InsertSessionProviderAccountingParams{
			SessionID: sessionID, RequestBatchID: entry.RequestBatchID,
			Version: 1, PayloadJson: entry.PayloadJSON,
			Checksum: providerAccountingChecksum(sessionID, entry),
		}); err != nil {
			return fmt.Errorf("save provider accounting: %w", err)
		}
	}
	return nil
}

func validateProviderAccounting(entry SessionProviderAccounting) error {
	if entry.Version != 1 {
		return ErrProviderAccountingVersion
	}
	if entry.RequestBatchID == "" || len(entry.PayloadJSON) > maxProviderAccountingPayload ||
		!utf8.Valid(entry.PayloadJSON) || !json.Valid(entry.PayloadJSON) {
		return ErrProviderAccountingInvalidRequest
	}
	return nil
}

func providerAccountingChecksum(sessionID string, entry SessionProviderAccounting) string {
	// Length framing binds the opaque bytes to their scope and envelope version.
	data := binary.BigEndian.AppendUint64(nil, uint64(len(sessionID)))
	data = append(data, sessionID...)
	data = binary.BigEndian.AppendUint64(data, uint64(len(entry.RequestBatchID)))
	data = append(data, entry.RequestBatchID...)
	data = binary.BigEndian.AppendUint64(data, entry.Version)
	data = append(data, entry.PayloadJSON...)
	return historyDigest(data)
}

func providerAccountingRecord(row gen.SessionProviderAccounting) (SessionProviderAccountingRecord, error) {
	if row.Version <= 0 {
		return SessionProviderAccountingRecord{}, ErrProviderAccountingCorrupt
	}
	if row.Version != 1 {
		return SessionProviderAccountingRecord{}, ErrProviderAccountingVersion
	}
	entry := SessionProviderAccounting{RequestBatchID: row.RequestBatchID, Version: uint64(row.Version), PayloadJSON: row.PayloadJson}
	if row.Sequence <= 0 || row.SessionID == "" || validateProviderAccounting(entry) != nil ||
		row.Checksum != providerAccountingChecksum(row.SessionID, entry) {
		return SessionProviderAccountingRecord{}, ErrProviderAccountingCorrupt
	}
	return SessionProviderAccountingRecord{Sequence: uint64(row.Sequence), Accounting: entry.Clone()}, nil
}

// GetSessionProviderAccounting returns an independently owned settlement scoped
// to sessionID and requestBatchID, or ErrNotFound. Corrupt or unsupported stored
// envelopes are rejected, never repaired or interpreted as zero usage.
func (s *Store) GetSessionProviderAccounting(ctx context.Context, sessionID, requestBatchID string) (SessionProviderAccountingRecord, error) {
	if sessionID == "" || requestBatchID == "" {
		return SessionProviderAccountingRecord{}, ErrProviderAccountingInvalidRequest
	}
	row, err := s.read.GetSessionProviderAccounting(ctx, gen.GetSessionProviderAccountingParams{
		SessionID: sessionID, RequestBatchID: requestBatchID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return SessionProviderAccountingRecord{}, ErrNotFound
	}
	if err != nil {
		return SessionProviderAccountingRecord{}, fmt.Errorf("get provider accounting: %w", err)
	}
	return providerAccountingRecord(gen.SessionProviderAccounting(row))
}

// ListSessionProviderAccounting returns independently owned settlements ordered
// by increasing opaque Sequence, strictly after the supplied cursor. Limit must
// be 1..256 and the cursor must fit a SQLite INTEGER; no unbounded default exists.
// A corrupt or unsupported envelope rejects the entire page.
func (s *Store) ListSessionProviderAccounting(ctx context.Context, sessionID string, afterSequence uint64, limit int) ([]SessionProviderAccountingRecord, error) {
	if sessionID == "" || afterSequence > math.MaxInt64 || limit < 1 || limit > 256 {
		return nil, ErrProviderAccountingInvalidRequest
	}
	rows, err := s.read.ListSessionProviderAccounting(ctx, gen.ListSessionProviderAccountingParams{
		SessionID: sessionID, AfterSequence: int64(afterSequence), PageLimit: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list provider accounting: %w", err)
	}
	records := make([]SessionProviderAccountingRecord, len(rows))
	for i, row := range rows {
		record, err := providerAccountingRecord(gen.SessionProviderAccounting(row))
		if err != nil {
			return nil, err
		}
		records[i] = record
	}
	return records, nil
}

// CommitCompletedTurnWithProviderAccounting commits the existing terminal
// session/transcript/history operation and settlements in one transaction.
// History receipts supplied here are available to the accounting writes.
func (s *Store) CommitCompletedTurnWithProviderAccounting(ctx context.Context, record SessionRecord, update TranscriptUpdate, accounting []SessionProviderAccounting, history ...SessionHistoryBatch) error {
	if err := validateProviderAccountingInput(update.SessionID, accounting); err != nil {
		return err
	}
	return s.updateActiveTranscript(ctx, update, &record, nil, history, cloneProviderAccounting(accounting))
}

// CommitCheckpointTurnWithProviderAccounting commits the existing guarded
// checkpoint-source operation and settlements in one transaction. A stale source
// or accounting error rolls back session, transcript, history, and accounting.
func (s *Store) CommitCheckpointTurnWithProviderAccounting(ctx context.Context, record SessionRecord, update TranscriptUpdate, expected SessionContentVersion, accounting []SessionProviderAccounting, history ...SessionHistoryBatch) error {
	if err := validateProviderAccountingInput(update.SessionID, accounting); err != nil {
		return err
	}
	return s.updateActiveTranscript(ctx, update, &record, &expected, history, cloneProviderAccounting(accounting))
}

// CommitCompletedTurnWithProviderAccountingVersioned retains every completed-turn
// guard and atomically commits history and settlements, returning the resulting
// session-row version from that same transaction. Failure returns a zero version.
func (s *Store) CommitCompletedTurnWithProviderAccountingVersioned(ctx context.Context, record SessionRecord, update TranscriptUpdate, expected SessionContentVersion, accounting []SessionProviderAccounting, history ...SessionHistoryBatch) (SessionContentVersion, error) {
	return s.CommitCompletedTurnWithAccountingVersioned(ctx, record, update, expected, accounting, nil, history...)
}

// CommitCheckpointTurnWithProviderAccountingVersioned retains every checkpoint
// source guard and atomically commits history and settlements, returning the
// resulting session-row version from that transaction. Failure returns zero.
func (s *Store) CommitCheckpointTurnWithProviderAccountingVersioned(ctx context.Context, record SessionRecord, update TranscriptUpdate, expected SessionContentVersion, accounting []SessionProviderAccounting, history ...SessionHistoryBatch) (SessionContentVersion, error) {
	return s.CommitCheckpointTurnWithAccountingVersioned(ctx, record, update, expected, accounting, nil, history...)
}

// CommitBeforeTurnWithProviderAccountingVersioned retains every BEFORE
// publication guard and commits prior settlements with their history receipts
// and checkpoint. It returns the session-row version from that same transaction
// only after commit; failure returns a zero version and publishes nothing.
func (s *Store) CommitBeforeTurnWithProviderAccountingVersioned(ctx context.Context, record SessionRecord, update TranscriptUpdate, boundary HistoryCheckpoint, expectedActive string, expected SessionContentVersion, accounting []SessionProviderAccounting, history ...SessionHistoryBatch) (SessionContentVersion, error) {
	return s.CommitBeforeTurnWithAccountingVersioned(ctx, record, update, boundary, expectedActive, expected, accounting, nil, history...)
}
