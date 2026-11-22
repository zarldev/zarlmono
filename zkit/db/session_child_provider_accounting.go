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

// ErrChildProviderAccountingConflict means a child attempt already has different evidence or ownership.
var ErrChildProviderAccountingConflict = errors.New("session child provider accounting conflict")

// ErrChildProviderAccountingCorrupt means stored child evidence failed envelope integrity checks.
var ErrChildProviderAccountingCorrupt = errors.New("session child provider accounting is corrupted")

// ErrChildProviderAccountingVersion means the child envelope version is unsupported.
var ErrChildProviderAccountingVersion = errors.New("unsupported session child provider accounting version")

// ErrChildProviderAccountingInvalidRequest means child input, scope, or pagination
// is invalid, or its owner request receipt does not exist in the same session.
var ErrChildProviderAccountingInvalidRequest = errors.New("invalid session child provider accounting request")

const maxChildProviderAccountingPayload = 1 << 20

// SessionChildProviderAccounting is immutable evidence for one child provider
// attempt. OwnerRequestBatchID anchors the recorded turn's first root request,
// not a child prepared request. Version 1 accepts opaque valid UTF-8 JSON up to
// 1 MiB; application payload semantics belong to the caller. PayloadJSON owns its bytes.
type SessionChildProviderAccounting struct {
	OwnerRequestBatchID string
	ChildTaskID         string
	Attempt             int
	Version             uint64
	PayloadJSON         []byte
}

// Clone returns an envelope with independent payload bytes.
func (r SessionChildProviderAccounting) Clone() SessionChildProviderAccounting {
	r.PayloadJSON = slices.Clone(r.PayloadJSON)
	return r
}

// SessionChildProviderAccountingRecord pairs child evidence with an increasing
// opaque cursor, not a contiguous count or per-session ordinal.
type SessionChildProviderAccountingRecord struct {
	Sequence   uint64
	Accounting SessionChildProviderAccounting
}

// SaveSessionChildProviderAccounting atomically saves child attempts for existing
// owner receipts in sessionID. Identical owner/version/bytes preserve the original
// cursor; changed evidence conflicts. A transaction-bound Store borrows the
// enclosing transaction. Inputs are borrowed until return and cloned before use;
// callers must not mutate them during the call.
func (s *Store) SaveSessionChildProviderAccounting(ctx context.Context, sessionID string, accounting []SessionChildProviderAccounting) error {
	if err := validateChildProviderAccountingInput(sessionID, accounting); err != nil {
		return err
	}
	accounting = cloneChildProviderAccounting(accounting)
	if s.inTx {
		return s.saveSessionChildProviderAccounting(ctx, sessionID, accounting)
	}
	return s.WithTx(ctx, func(tx *Store) error {
		return tx.saveSessionChildProviderAccounting(ctx, sessionID, accounting)
	})
}

func validateChildProviderAccountingInput(sessionID string, accounting []SessionChildProviderAccounting) error {
	if sessionID == "" {
		return ErrChildProviderAccountingInvalidRequest
	}
	for _, entry := range accounting {
		if err := validateChildProviderAccounting(entry); err != nil {
			return err
		}
	}
	return nil
}

func validateChildProviderAccounting(entry SessionChildProviderAccounting) error {
	if entry.Version != 1 {
		return ErrChildProviderAccountingVersion
	}
	if entry.OwnerRequestBatchID == "" || entry.ChildTaskID == "" || entry.Attempt <= 0 ||
		uint64(entry.Attempt) > math.MaxInt64 || len(entry.PayloadJSON) > maxChildProviderAccountingPayload ||
		!utf8.Valid(entry.PayloadJSON) || !json.Valid(entry.PayloadJSON) {
		return ErrChildProviderAccountingInvalidRequest
	}
	return nil
}

func cloneChildProviderAccounting(accounting []SessionChildProviderAccounting) []SessionChildProviderAccounting {
	owned := make([]SessionChildProviderAccounting, len(accounting))
	for i, entry := range accounting {
		owned[i] = entry.Clone()
	}
	return owned
}

// saveSessionChildProviderAccounting requires a transaction-bound Store.
func (s *Store) saveSessionChildProviderAccounting(ctx context.Context, sessionID string, accounting []SessionChildProviderAccounting) error {
	if len(accounting) == 0 {
		return nil
	}
	// Take the shared SQLite writer before any deduplication or receipt reads,
	// including when independently opened Stores compete for the same attempt.
	if err := s.q.AcquireCheckpointWrite(ctx); err != nil {
		return fmt.Errorf("acquire child accounting write: %w", err)
	}
	for _, entry := range accounting {
		previous, err := s.q.GetSessionChildProviderAccounting(ctx, gen.GetSessionChildProviderAccountingParams{
			SessionID: sessionID, ChildTaskID: entry.ChildTaskID, Attempt: int64(entry.Attempt),
		})
		if err == nil {
			record, err := childProviderAccountingRecord(gen.SessionChildProviderAccounting(previous))
			if err != nil {
				return err
			}
			if record.Accounting.OwnerRequestBatchID != entry.OwnerRequestBatchID ||
				record.Accounting.Version != entry.Version || !bytes.Equal(record.Accounting.PayloadJSON, entry.PayloadJSON) {
				return ErrChildProviderAccountingConflict
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read child accounting receipt: %w", err)
		}
		if _, err := s.q.GetHistoryBatch(ctx, gen.GetHistoryBatchParams{SessionID: sessionID, BatchID: entry.OwnerRequestBatchID}); errors.Is(err, sql.ErrNoRows) {
			return ErrChildProviderAccountingInvalidRequest
		} else if err != nil {
			return fmt.Errorf("read child accounting owner receipt: %w", err)
		}
		// Input validation accepts only envelope version 1.
		if err := s.q.InsertSessionChildProviderAccounting(ctx, gen.InsertSessionChildProviderAccountingParams{
			SessionID: sessionID, OwnerRequestBatchID: entry.OwnerRequestBatchID,
			ChildTaskID: entry.ChildTaskID, Attempt: int64(entry.Attempt), Version: 1,
			PayloadJson: entry.PayloadJSON, Checksum: childProviderAccountingChecksum(sessionID, entry),
		}); err != nil {
			return fmt.Errorf("save child provider accounting: %w", err)
		}
	}
	return nil
}

func childProviderAccountingChecksum(sessionID string, entry SessionChildProviderAccounting) string {
	// Length-frame every field, including numeric representations and payload,
	// so integrity binds the complete session/turn/attempt identity to exact bytes.
	var data []byte
	for _, field := range [][]byte{
		[]byte(sessionID), []byte(entry.OwnerRequestBatchID), []byte(entry.ChildTaskID),
		binary.BigEndian.AppendUint64(nil, uint64(entry.Attempt)), // #nosec G115 -- callers validate that Attempt is positive and fits SQLite INTEGER.
		binary.BigEndian.AppendUint64(nil, entry.Version), entry.PayloadJSON,
	} {
		data = binary.BigEndian.AppendUint64(data, uint64(len(field)))
		data = append(data, field...)
	}
	return historyDigest(data)
}

func childProviderAccountingRecord(row gen.SessionChildProviderAccounting) (SessionChildProviderAccountingRecord, error) {
	if row.Version <= 0 {
		return SessionChildProviderAccountingRecord{}, ErrChildProviderAccountingCorrupt
	}
	if row.Version != 1 {
		return SessionChildProviderAccountingRecord{}, ErrChildProviderAccountingVersion
	}
	if row.Attempt <= 0 || uint64(row.Attempt) > uint64(math.MaxInt) {
		return SessionChildProviderAccountingRecord{}, ErrChildProviderAccountingCorrupt
	}
	entry := SessionChildProviderAccounting{
		OwnerRequestBatchID: row.OwnerRequestBatchID, ChildTaskID: row.ChildTaskID,
		Attempt: int(row.Attempt), Version: uint64(row.Version), PayloadJSON: row.PayloadJson,
	}
	if row.Sequence <= 0 || row.SessionID == "" || validateChildProviderAccounting(entry) != nil ||
		row.Checksum != childProviderAccountingChecksum(row.SessionID, entry) {
		return SessionChildProviderAccountingRecord{}, ErrChildProviderAccountingCorrupt
	}
	return SessionChildProviderAccountingRecord{Sequence: uint64(row.Sequence), Accounting: entry.Clone()}, nil
}

// GetSessionChildProviderAccounting returns owned child evidence for the scoped
// attempt, or ErrNotFound. Corrupt or unsupported envelopes are never repaired.
func (s *Store) GetSessionChildProviderAccounting(ctx context.Context, sessionID, childTaskID string, attempt int) (SessionChildProviderAccountingRecord, error) {
	if sessionID == "" || childTaskID == "" || attempt <= 0 || uint64(attempt) > math.MaxInt64 {
		return SessionChildProviderAccountingRecord{}, ErrChildProviderAccountingInvalidRequest
	}
	row, err := s.read.GetSessionChildProviderAccounting(ctx, gen.GetSessionChildProviderAccountingParams{
		SessionID: sessionID, ChildTaskID: childTaskID, Attempt: int64(attempt),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return SessionChildProviderAccountingRecord{}, ErrNotFound
	}
	if err != nil {
		return SessionChildProviderAccountingRecord{}, fmt.Errorf("get child provider accounting: %w", err)
	}
	return childProviderAccountingRecord(gen.SessionChildProviderAccounting(row))
}

// ListSessionChildProviderAccounting returns owned evidence ordered strictly
// after an opaque Sequence cursor. The cursor must fit SQLite INTEGER and limit
// must be 1..256. Any corrupt or unsupported envelope rejects the entire page.
func (s *Store) ListSessionChildProviderAccounting(ctx context.Context, sessionID string, afterSequence uint64, limit int) ([]SessionChildProviderAccountingRecord, error) {
	if sessionID == "" || afterSequence > math.MaxInt64 || limit < 1 || limit > 256 {
		return nil, ErrChildProviderAccountingInvalidRequest
	}
	rows, err := s.read.ListSessionChildProviderAccounting(ctx, gen.ListSessionChildProviderAccountingParams{
		SessionID: sessionID, AfterSequence: int64(afterSequence), PageLimit: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list child provider accounting: %w", err)
	}
	records := make([]SessionChildProviderAccountingRecord, len(rows))
	for i, row := range rows {
		record, err := childProviderAccountingRecord(gen.SessionChildProviderAccounting(row))
		if err != nil {
			return nil, err
		}
		records[i] = record
	}
	return records, nil
}
