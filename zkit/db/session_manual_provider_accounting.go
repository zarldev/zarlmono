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

// ErrManualProviderAccountingInvalidRequest indicates invalid scope, envelope, or page.
var ErrManualProviderAccountingInvalidRequest = errors.New("invalid session manual provider accounting request")

// ErrManualProviderAccountingConflict indicates differing evidence for an invocation.
var ErrManualProviderAccountingConflict = errors.New("session manual provider accounting conflict")

// ErrManualProviderAccountingCorrupt indicates invalid stored envelope integrity.
var ErrManualProviderAccountingCorrupt = errors.New("session manual provider accounting is corrupted")

// ErrManualProviderAccountingVersion indicates an unsupported envelope version.
var ErrManualProviderAccountingVersion = errors.New("unsupported session manual provider accounting version")

const maxManualProviderAccountingPayload = 1 << 20

// SessionManualProviderAccounting is immutable evidence for a manual invocation.
// PayloadJSON is owned by the value; its semantics belong to the caller.
type SessionManualProviderAccounting struct {
	InvocationID string
	Version      uint64
	PayloadJSON  []byte
}

// Clone returns independently owned payload bytes.
func (r SessionManualProviderAccounting) Clone() SessionManualProviderAccounting {
	r.PayloadJSON = slices.Clone(r.PayloadJSON)
	return r
}

// SessionManualProviderAccountingRecord pairs evidence with an opaque increasing cursor.
type SessionManualProviderAccountingRecord struct {
	Sequence   uint64
	Accounting SessionManualProviderAccounting
}

func validateManualProviderAccountingInput(sessionID string, accounting []SessionManualProviderAccounting) error {
	if sessionID == "" || !utf8.ValidString(sessionID) {
		return ErrManualProviderAccountingInvalidRequest
	}
	for _, entry := range accounting {
		if entry.Version != 1 {
			return ErrManualProviderAccountingVersion
		}
		if entry.InvocationID == "" || !utf8.ValidString(entry.InvocationID) || len(entry.PayloadJSON) > maxManualProviderAccountingPayload || !utf8.Valid(entry.PayloadJSON) || !json.Valid(entry.PayloadJSON) {
			return ErrManualProviderAccountingInvalidRequest
		}
	}
	return nil
}

func cloneManualProviderAccounting(accounting []SessionManualProviderAccounting) []SessionManualProviderAccounting {
	owned := make([]SessionManualProviderAccounting, len(accounting))
	for i, entry := range accounting {
		owned[i] = entry.Clone()
	}
	return owned
}

func manualProviderAccountingChecksum(sessionID string, entry SessionManualProviderAccounting) string {
	var data []byte
	for _, field := range [][]byte{[]byte(sessionID), []byte(entry.InvocationID), binary.BigEndian.AppendUint64(nil, entry.Version), entry.PayloadJSON} {
		data = binary.BigEndian.AppendUint64(data, uint64(len(field)))
		data = append(data, field...)
	}
	return historyDigest(data)
}

func manualProviderAccountingRecord(row gen.SessionManualProviderAccounting) (SessionManualProviderAccountingRecord, error) {
	if row.Version <= 0 {
		return SessionManualProviderAccountingRecord{}, ErrManualProviderAccountingCorrupt
	}
	if row.Version != 1 {
		return SessionManualProviderAccountingRecord{}, ErrManualProviderAccountingVersion
	}
	entry := SessionManualProviderAccounting{InvocationID: row.InvocationID, Version: uint64(row.Version), PayloadJSON: row.PayloadJson}
	if row.Sequence <= 0 || validateManualProviderAccountingInput(row.SessionID, []SessionManualProviderAccounting{entry}) != nil || row.Checksum != manualProviderAccountingChecksum(row.SessionID, entry) {
		return SessionManualProviderAccountingRecord{}, ErrManualProviderAccountingCorrupt
	}
	return SessionManualProviderAccountingRecord{Sequence: uint64(row.Sequence), Accounting: entry.Clone()}, nil
}

// saveSessionManualProviderAccounting requires the enclosing transaction and preserves
// identical evidence's original cursor; it never creates a session or a receipt.
func (s *Store) saveSessionManualProviderAccounting(ctx context.Context, sessionID string, accounting []SessionManualProviderAccounting) error {
	if len(accounting) == 0 {
		return nil
	}
	if err := s.q.AcquireCheckpointWrite(ctx); err != nil {
		return fmt.Errorf("acquire manual accounting write: %w", err)
	}
	for _, entry := range accounting {
		previous, err := s.q.GetSessionManualProviderAccounting(ctx, gen.GetSessionManualProviderAccountingParams{SessionID: sessionID, InvocationID: entry.InvocationID})
		if err == nil {
			record, err := manualProviderAccountingRecord(gen.SessionManualProviderAccounting(previous))
			if err != nil {
				return err
			}
			if record.Accounting.Version != entry.Version || !bytes.Equal(record.Accounting.PayloadJSON, entry.PayloadJSON) {
				return ErrManualProviderAccountingConflict
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read manual provider accounting: %w", err)
		}
		if err := s.q.InsertSessionManualProviderAccounting(ctx, gen.InsertSessionManualProviderAccountingParams{SessionID: sessionID, InvocationID: entry.InvocationID, Version: 1, PayloadJson: entry.PayloadJSON, Checksum: manualProviderAccountingChecksum(sessionID, entry)}); err != nil {
			return fmt.Errorf("save manual provider accounting: %w", err)
		}
	}
	return nil
}

// GetSessionManualProviderAccounting returns owned evidence scoped by session and invocation, or ErrNotFound.
func (s *Store) GetSessionManualProviderAccounting(ctx context.Context, sessionID, invocationID string) (SessionManualProviderAccountingRecord, error) {
	if sessionID == "" || invocationID == "" || !utf8.ValidString(sessionID) || !utf8.ValidString(invocationID) {
		return SessionManualProviderAccountingRecord{}, ErrManualProviderAccountingInvalidRequest
	}
	row, err := s.read.GetSessionManualProviderAccounting(ctx, gen.GetSessionManualProviderAccountingParams{SessionID: sessionID, InvocationID: invocationID})
	if errors.Is(err, sql.ErrNoRows) {
		return SessionManualProviderAccountingRecord{}, ErrNotFound
	}
	if err != nil {
		return SessionManualProviderAccountingRecord{}, fmt.Errorf("get manual provider accounting: %w", err)
	}
	return manualProviderAccountingRecord(gen.SessionManualProviderAccounting(row))
}

// ListSessionManualProviderAccounting returns owned evidence after an opaque cursor;
// the page limit is 1..256 and any corrupt row rejects the whole page.
func (s *Store) ListSessionManualProviderAccounting(ctx context.Context, sessionID string, afterSequence uint64, limit int) ([]SessionManualProviderAccountingRecord, error) {
	if sessionID == "" || !utf8.ValidString(sessionID) || afterSequence > math.MaxInt64 || limit < 1 || limit > 256 {
		return nil, ErrManualProviderAccountingInvalidRequest
	}
	rows, err := s.read.ListSessionManualProviderAccounting(ctx, gen.ListSessionManualProviderAccountingParams{SessionID: sessionID, AfterSequence: int64(afterSequence), PageLimit: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("list manual provider accounting: %w", err)
	}
	records := make([]SessionManualProviderAccountingRecord, len(rows))
	for i, row := range rows {
		record, err := manualProviderAccountingRecord(gen.SessionManualProviderAccounting(row))
		if err != nil {
			return nil, err
		}
		records[i] = record
	}
	return records, nil
}
