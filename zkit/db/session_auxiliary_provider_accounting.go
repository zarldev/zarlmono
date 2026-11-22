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

// ErrAuxiliaryProviderAccountingConflict means an auxiliary invocation already has different evidence or ownership.
var ErrAuxiliaryProviderAccountingConflict = errors.New("session auxiliary provider accounting conflict")

// ErrAuxiliaryProviderAccountingCorrupt means stored auxiliary evidence failed envelope integrity checks.
var ErrAuxiliaryProviderAccountingCorrupt = errors.New("session auxiliary provider accounting is corrupted")

// ErrAuxiliaryProviderAccountingVersion means the auxiliary envelope version is unsupported.
var ErrAuxiliaryProviderAccountingVersion = errors.New("unsupported session auxiliary provider accounting version")

// ErrAuxiliaryProviderAccountingInvalidRequest means auxiliary input, scope, or pagination
// is invalid, or its owner request receipt does not exist in the same session.
var ErrAuxiliaryProviderAccountingInvalidRequest = errors.New("invalid session auxiliary provider accounting request")

const maxAuxiliaryProviderAccountingPayload = 1 << 20

// SessionAuxiliaryProviderAccounting is immutable evidence for one auxiliary provider
// stream invocation. OwnerRequestBatchID anchors the recorded turn's first root request,
// not an auxiliary prepared request. Version 1 accepts opaque valid UTF-8 JSON up to
// 1 MiB; application payload semantics belong to the caller. The database proves
// only same-session receipt ownership, not that it was the turn's first root
// receipt. InvocationID is opaque here. PayloadJSON owns its bytes.
type SessionAuxiliaryProviderAccounting struct {
	InvocationID        string
	OwnerRequestBatchID string
	Version             uint64
	PayloadJSON         []byte
}

// Clone returns an envelope with independent payload bytes.
func (r SessionAuxiliaryProviderAccounting) Clone() SessionAuxiliaryProviderAccounting {
	r.PayloadJSON = slices.Clone(r.PayloadJSON)
	return r
}

// SessionAuxiliaryProviderAccountingRecord pairs auxiliary evidence with an increasing
// opaque cursor, not a contiguous count or per-session ordinal.
type SessionAuxiliaryProviderAccountingRecord struct {
	Sequence   uint64
	Accounting SessionAuxiliaryProviderAccounting
}

// SaveSessionAuxiliaryProviderAccounting atomically saves auxiliary invocations for existing
// owner receipts in sessionID. Identical owner/version/bytes preserve the original
// cursor; changed evidence conflicts. A transaction-bound Store borrows the
// enclosing transaction. Inputs are borrowed until return and cloned before use;
// callers must not mutate them during the call.
func (s *Store) SaveSessionAuxiliaryProviderAccounting(ctx context.Context, sessionID string, accounting []SessionAuxiliaryProviderAccounting) error {
	if err := validateAuxiliaryProviderAccountingInput(sessionID, accounting); err != nil {
		return err
	}
	accounting = cloneAuxiliaryProviderAccounting(accounting)
	if s.inTx {
		return s.saveSessionAuxiliaryProviderAccounting(ctx, sessionID, accounting)
	}
	return s.WithTx(ctx, func(tx *Store) error {
		return tx.saveSessionAuxiliaryProviderAccounting(ctx, sessionID, accounting)
	})
}

func validateAuxiliaryProviderAccountingInput(sessionID string, accounting []SessionAuxiliaryProviderAccounting) error {
	if sessionID == "" || !utf8.ValidString(sessionID) {
		return ErrAuxiliaryProviderAccountingInvalidRequest
	}
	for _, entry := range accounting {
		if err := validateAuxiliaryProviderAccounting(entry); err != nil {
			return err
		}
	}
	return nil
}

func validateAuxiliaryProviderAccounting(entry SessionAuxiliaryProviderAccounting) error {
	if entry.Version != 1 {
		return ErrAuxiliaryProviderAccountingVersion
	}
	if entry.OwnerRequestBatchID == "" || entry.InvocationID == "" ||
		!utf8.ValidString(entry.OwnerRequestBatchID) || !utf8.ValidString(entry.InvocationID) || len(entry.PayloadJSON) > maxAuxiliaryProviderAccountingPayload ||
		!utf8.Valid(entry.PayloadJSON) || !json.Valid(entry.PayloadJSON) {
		return ErrAuxiliaryProviderAccountingInvalidRequest
	}
	return nil
}

func cloneAuxiliaryProviderAccounting(accounting []SessionAuxiliaryProviderAccounting) []SessionAuxiliaryProviderAccounting {
	owned := make([]SessionAuxiliaryProviderAccounting, len(accounting))
	for i, entry := range accounting {
		owned[i] = entry.Clone()
	}
	return owned
}

// saveSessionAuxiliaryProviderAccounting requires a transaction-bound Store.
func (s *Store) saveSessionAuxiliaryProviderAccounting(ctx context.Context, sessionID string, accounting []SessionAuxiliaryProviderAccounting) error {
	if len(accounting) == 0 {
		return nil
	}
	// Take the shared SQLite writer before any deduplication or receipt reads,
	// including when independently opened Stores compete for the same invocation.
	if err := s.q.AcquireCheckpointWrite(ctx); err != nil {
		return fmt.Errorf("acquire auxiliary accounting write: %w", err)
	}
	for _, entry := range accounting {
		previous, err := s.q.GetSessionAuxiliaryProviderAccounting(ctx, gen.GetSessionAuxiliaryProviderAccountingParams{
			SessionID: sessionID, InvocationID: entry.InvocationID,
		})
		if err == nil {
			record, err := auxiliaryProviderAccountingRecord(gen.SessionAuxiliaryProviderAccounting(previous))
			if err != nil {
				return err
			}
			if record.Accounting.OwnerRequestBatchID != entry.OwnerRequestBatchID ||
				record.Accounting.Version != entry.Version || !bytes.Equal(record.Accounting.PayloadJSON, entry.PayloadJSON) {
				return ErrAuxiliaryProviderAccountingConflict
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read auxiliary accounting receipt: %w", err)
		}
		if _, err := s.q.GetHistoryBatch(ctx, gen.GetHistoryBatchParams{SessionID: sessionID, BatchID: entry.OwnerRequestBatchID}); errors.Is(err, sql.ErrNoRows) {
			return ErrAuxiliaryProviderAccountingInvalidRequest
		} else if err != nil {
			return fmt.Errorf("read auxiliary accounting owner receipt: %w", err)
		}
		// Input validation accepts only envelope version 1.
		if err := s.q.InsertSessionAuxiliaryProviderAccounting(ctx, gen.InsertSessionAuxiliaryProviderAccountingParams{
			SessionID: sessionID, OwnerRequestBatchID: entry.OwnerRequestBatchID,
			InvocationID: entry.InvocationID, Version: 1,
			PayloadJson: entry.PayloadJSON, Checksum: auxiliaryProviderAccountingChecksum(sessionID, entry),
		}); err != nil {
			return fmt.Errorf("save auxiliary provider accounting: %w", err)
		}
	}
	return nil
}

func auxiliaryProviderAccountingChecksum(sessionID string, entry SessionAuxiliaryProviderAccounting) string {
	// Length-frame every field, including numeric representations and payload,
	// so integrity binds the complete session/invocation/owner identity to exact bytes.
	var data []byte
	for _, field := range [][]byte{
		[]byte(sessionID), []byte(entry.InvocationID), []byte(entry.OwnerRequestBatchID),
		binary.BigEndian.AppendUint64(nil, entry.Version), entry.PayloadJSON,
	} {
		data = binary.BigEndian.AppendUint64(data, uint64(len(field)))
		data = append(data, field...)
	}
	return historyDigest(data)
}

func auxiliaryProviderAccountingRecord(row gen.SessionAuxiliaryProviderAccounting) (SessionAuxiliaryProviderAccountingRecord, error) {
	if row.Version <= 0 {
		return SessionAuxiliaryProviderAccountingRecord{}, ErrAuxiliaryProviderAccountingCorrupt
	}
	if row.Version != 1 {
		return SessionAuxiliaryProviderAccountingRecord{}, ErrAuxiliaryProviderAccountingVersion
	}
	entry := SessionAuxiliaryProviderAccounting{
		OwnerRequestBatchID: row.OwnerRequestBatchID, InvocationID: row.InvocationID,
		Version: uint64(row.Version), PayloadJSON: row.PayloadJson,
	}
	if row.Sequence <= 0 || row.SessionID == "" || !utf8.ValidString(row.SessionID) || validateAuxiliaryProviderAccounting(entry) != nil ||
		row.Checksum != auxiliaryProviderAccountingChecksum(row.SessionID, entry) {
		return SessionAuxiliaryProviderAccountingRecord{}, ErrAuxiliaryProviderAccountingCorrupt
	}
	return SessionAuxiliaryProviderAccountingRecord{Sequence: uint64(row.Sequence), Accounting: entry.Clone()}, nil
}

// GetSessionAuxiliaryProviderAccounting returns owned auxiliary evidence for the scoped
// invocation, or ErrNotFound. Corrupt or unsupported envelopes are never repaired.
func (s *Store) GetSessionAuxiliaryProviderAccounting(ctx context.Context, sessionID, invocationID string) (SessionAuxiliaryProviderAccountingRecord, error) {
	if sessionID == "" || invocationID == "" || !utf8.ValidString(sessionID) || !utf8.ValidString(invocationID) {
		return SessionAuxiliaryProviderAccountingRecord{}, ErrAuxiliaryProviderAccountingInvalidRequest
	}
	row, err := s.read.GetSessionAuxiliaryProviderAccounting(ctx, gen.GetSessionAuxiliaryProviderAccountingParams{
		SessionID: sessionID, InvocationID: invocationID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return SessionAuxiliaryProviderAccountingRecord{}, ErrNotFound
	}
	if err != nil {
		return SessionAuxiliaryProviderAccountingRecord{}, fmt.Errorf("get auxiliary provider accounting: %w", err)
	}
	return auxiliaryProviderAccountingRecord(gen.SessionAuxiliaryProviderAccounting(row))
}

// ListSessionAuxiliaryProviderAccounting returns owned evidence ordered strictly
// after an opaque Sequence cursor. The cursor must fit SQLite INTEGER and limit
// must be 1..256. Any corrupt or unsupported envelope rejects the entire page.
func (s *Store) ListSessionAuxiliaryProviderAccounting(ctx context.Context, sessionID string, afterSequence uint64, limit int) ([]SessionAuxiliaryProviderAccountingRecord, error) {
	if sessionID == "" || !utf8.ValidString(sessionID) || afterSequence > math.MaxInt64 || limit < 1 || limit > 256 {
		return nil, ErrAuxiliaryProviderAccountingInvalidRequest
	}
	rows, err := s.read.ListSessionAuxiliaryProviderAccounting(ctx, gen.ListSessionAuxiliaryProviderAccountingParams{
		SessionID: sessionID, AfterSequence: int64(afterSequence), PageLimit: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list auxiliary provider accounting: %w", err)
	}
	records := make([]SessionAuxiliaryProviderAccountingRecord, len(rows))
	for i, row := range rows {
		record, err := auxiliaryProviderAccountingRecord(gen.SessionAuxiliaryProviderAccounting(row))
		if err != nil {
			return nil, err
		}
		records[i] = record
	}
	return records, nil
}
