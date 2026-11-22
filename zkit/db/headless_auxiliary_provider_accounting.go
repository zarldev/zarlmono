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

// ErrHeadlessAuxiliaryProviderAccountingConflict means an auxiliary invocation already has different evidence.
var ErrHeadlessAuxiliaryProviderAccountingConflict = errors.New("headless auxiliary provider accounting conflict")

// ErrHeadlessAuxiliaryProviderAccountingCorrupt means stored auxiliary evidence failed envelope integrity checks.
var ErrHeadlessAuxiliaryProviderAccountingCorrupt = errors.New("headless auxiliary provider accounting is corrupted")

// ErrHeadlessAuxiliaryProviderAccountingVersion means the auxiliary envelope version is unsupported.
var ErrHeadlessAuxiliaryProviderAccountingVersion = errors.New("unsupported headless auxiliary provider accounting version")

// ErrHeadlessAuxiliaryProviderAccountingInvalidRequest means auxiliary input, scope, or pagination
// is invalid.
var ErrHeadlessAuxiliaryProviderAccountingInvalidRequest = errors.New("invalid headless auxiliary provider accounting request")

const maxHeadlessAuxiliaryProviderAccountingPayload = 1 << 20

// HeadlessAuxiliaryProviderAccounting is immutable evidence for one auxiliary provider
// stream invocation within a headless run. Version 1 accepts opaque valid UTF-8
// JSON up to 1 MiB; application payload semantics belong to the caller.
// InvocationID is opaque here. Value copies borrow PayloadJSON; Clone owns bytes.
type HeadlessAuxiliaryProviderAccounting struct {
	InvocationID string
	Version      uint64
	PayloadJSON  []byte
}

// Clone returns an envelope with independent payload bytes.
func (r HeadlessAuxiliaryProviderAccounting) Clone() HeadlessAuxiliaryProviderAccounting {
	r.PayloadJSON = slices.Clone(r.PayloadJSON)
	return r
}

// HeadlessAuxiliaryProviderAccountingRecord pairs auxiliary evidence with an increasing
// opaque cursor, not a contiguous count or per-run ordinal.
type HeadlessAuxiliaryProviderAccountingRecord struct {
	Sequence   uint64
	Accounting HeadlessAuxiliaryProviderAccounting
}

func validateHeadlessAuxiliaryProviderAccounting(entry HeadlessAuxiliaryProviderAccounting) error {
	if entry.Version != 1 {
		return ErrHeadlessAuxiliaryProviderAccountingVersion
	}
	if entry.InvocationID == "" ||
		!utf8.ValidString(entry.InvocationID) || len(entry.PayloadJSON) > maxHeadlessAuxiliaryProviderAccountingPayload ||
		!utf8.Valid(entry.PayloadJSON) || !json.Valid(entry.PayloadJSON) {
		return ErrHeadlessAuxiliaryProviderAccountingInvalidRequest
	}
	return nil
}

func cloneHeadlessAuxiliaryProviderAccounting(accounting []HeadlessAuxiliaryProviderAccounting) []HeadlessAuxiliaryProviderAccounting {
	owned := make([]HeadlessAuxiliaryProviderAccounting, len(accounting))
	for i, entry := range accounting {
		owned[i] = entry.Clone()
	}
	return owned
}

// saveHeadlessAuxiliaryProviderAccounting requires a transaction-bound Store.
func (s *Store) saveHeadlessAuxiliaryProviderAccounting(ctx context.Context, runID string, accounting []HeadlessAuxiliaryProviderAccounting) error {
	if len(accounting) == 0 {
		return nil
	}
	// The batch has already acquired the writer by ensuring its frozen start.
	for _, entry := range accounting {
		previous, err := s.q.GetHeadlessAuxiliaryProviderAccounting(ctx, gen.GetHeadlessAuxiliaryProviderAccountingParams{
			RunID: runID, InvocationID: entry.InvocationID,
		})
		if err == nil {
			record, err := headlessAuxiliaryProviderAccountingRecord(gen.HeadlessAuxiliaryProviderAccounting(previous))
			if err != nil {
				return err
			}
			if record.Accounting.Version != entry.Version || !bytes.Equal(record.Accounting.PayloadJSON, entry.PayloadJSON) {
				return ErrHeadlessAuxiliaryProviderAccountingConflict
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read auxiliary accounting receipt: %w", err)
		}
		// Input validation accepts only envelope version 1.
		if err := s.q.InsertHeadlessAuxiliaryProviderAccounting(ctx, gen.InsertHeadlessAuxiliaryProviderAccountingParams{
			RunID:        runID,
			InvocationID: entry.InvocationID, Version: 1,
			PayloadJson: entry.PayloadJSON, Checksum: headlessAuxiliaryProviderAccountingChecksum(runID, entry),
		}); err != nil {
			return fmt.Errorf("save auxiliary provider accounting: %w", err)
		}
	}
	return nil
}

func headlessAuxiliaryProviderAccountingChecksum(runID string, entry HeadlessAuxiliaryProviderAccounting) string {
	// Length-frame every field, including numeric representations and payload,
	// so integrity binds the complete run/invocation identity to exact bytes.
	var data []byte
	for _, field := range [][]byte{
		[]byte(runID), []byte(entry.InvocationID),
		binary.BigEndian.AppendUint64(nil, entry.Version), entry.PayloadJSON,
	} {
		data = binary.BigEndian.AppendUint64(data, uint64(len(field)))
		data = append(data, field...)
	}
	return historyDigest(data)
}

func headlessAuxiliaryProviderAccountingRecord(row gen.HeadlessAuxiliaryProviderAccounting) (HeadlessAuxiliaryProviderAccountingRecord, error) {
	if row.Version <= 0 {
		return HeadlessAuxiliaryProviderAccountingRecord{}, ErrHeadlessAuxiliaryProviderAccountingCorrupt
	}
	if row.Version != 1 {
		return HeadlessAuxiliaryProviderAccountingRecord{}, ErrHeadlessAuxiliaryProviderAccountingVersion
	}
	entry := HeadlessAuxiliaryProviderAccounting{
		InvocationID: row.InvocationID,
		Version:      uint64(row.Version), PayloadJSON: row.PayloadJson,
	}
	if row.Sequence <= 0 || row.RunID == "" || !utf8.ValidString(row.RunID) || validateHeadlessAuxiliaryProviderAccounting(entry) != nil ||
		row.Checksum != headlessAuxiliaryProviderAccountingChecksum(row.RunID, entry) {
		return HeadlessAuxiliaryProviderAccountingRecord{}, ErrHeadlessAuxiliaryProviderAccountingCorrupt
	}
	return HeadlessAuxiliaryProviderAccountingRecord{Sequence: uint64(row.Sequence), Accounting: entry.Clone()}, nil
}

// GetHeadlessAuxiliaryProviderAccounting returns owned auxiliary evidence for the scoped
// invocation, or ErrNotFound. Corrupt or unsupported envelopes are never repaired.
func (s *Store) GetHeadlessAuxiliaryProviderAccounting(ctx context.Context, runID, invocationID string) (HeadlessAuxiliaryProviderAccountingRecord, error) {
	if runID == "" || invocationID == "" || !utf8.ValidString(runID) || !utf8.ValidString(invocationID) {
		return HeadlessAuxiliaryProviderAccountingRecord{}, ErrHeadlessAuxiliaryProviderAccountingInvalidRequest
	}
	row, err := s.read.GetHeadlessAuxiliaryProviderAccounting(ctx, gen.GetHeadlessAuxiliaryProviderAccountingParams{
		RunID: runID, InvocationID: invocationID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return HeadlessAuxiliaryProviderAccountingRecord{}, ErrNotFound
	}
	if err != nil {
		return HeadlessAuxiliaryProviderAccountingRecord{}, fmt.Errorf("get auxiliary provider accounting: %w", err)
	}
	return headlessAuxiliaryProviderAccountingRecord(gen.HeadlessAuxiliaryProviderAccounting(row))
}

// ListHeadlessAuxiliaryProviderAccounting returns owned evidence ordered strictly
// after an opaque Sequence cursor. The cursor must fit SQLite INTEGER and limit
// must be 1..256. Any corrupt or unsupported envelope rejects the entire page.
func (s *Store) ListHeadlessAuxiliaryProviderAccounting(ctx context.Context, runID string, afterSequence uint64, limit int) ([]HeadlessAuxiliaryProviderAccountingRecord, error) {
	if runID == "" || !utf8.ValidString(runID) || afterSequence > math.MaxInt64 || limit < 1 || limit > 256 {
		return nil, ErrHeadlessAuxiliaryProviderAccountingInvalidRequest
	}
	rows, err := s.read.ListHeadlessAuxiliaryProviderAccounting(ctx, gen.ListHeadlessAuxiliaryProviderAccountingParams{
		RunID: runID, AfterSequence: int64(afterSequence), PageLimit: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list auxiliary provider accounting: %w", err)
	}
	records := make([]HeadlessAuxiliaryProviderAccountingRecord, len(rows))
	for i, row := range rows {
		record, err := headlessAuxiliaryProviderAccountingRecord(gen.HeadlessAuxiliaryProviderAccounting(row))
		if err != nil {
			return nil, err
		}
		records[i] = record
	}
	return records, nil
}
