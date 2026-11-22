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

// ErrHeadlessChildProviderAccountingInvalidRequest means a child identity, payload, or page is invalid.
var ErrHeadlessChildProviderAccountingInvalidRequest = errors.New("invalid headless child provider accounting request")

// ErrHeadlessChildProviderAccountingConflict means a child attempt already has different evidence.
var ErrHeadlessChildProviderAccountingConflict = errors.New("headless child provider accounting conflict")

// ErrHeadlessChildProviderAccountingCorrupt means stored child evidence failed envelope integrity checks.
var ErrHeadlessChildProviderAccountingCorrupt = errors.New("headless child provider accounting is corrupted")

// ErrHeadlessChildProviderAccountingVersion means the stored or supplied envelope version is unsupported.
var ErrHeadlessChildProviderAccountingVersion = errors.New("unsupported headless child provider accounting version")

const maxHeadlessChildProviderAccountingPayload = 1 << 20

// HeadlessChildProviderAccounting is opaque versioned JSON evidence for one child stream attempt.
// Value copies borrow PayloadJSON; Clone owns its bytes.
type HeadlessChildProviderAccounting struct {
	ChildTaskID string
	Attempt     int
	Version     uint64
	PayloadJSON []byte
}

// Clone returns independent payload bytes.
func (r HeadlessChildProviderAccounting) Clone() HeadlessChildProviderAccounting {
	r.PayloadJSON = slices.Clone(r.PayloadJSON)
	return r
}

// HeadlessChildProviderAccountingRecord pairs evidence with an increasing opaque cursor.
type HeadlessChildProviderAccountingRecord struct {
	Sequence   uint64
	Accounting HeadlessChildProviderAccounting
}

func validateHeadlessChildProviderAccounting(entry HeadlessChildProviderAccounting) error {
	if entry.Version != 1 {
		return ErrHeadlessChildProviderAccountingVersion
	}
	if entry.ChildTaskID == "" || !utf8.ValidString(entry.ChildTaskID) || !validHeadlessAccountingOrdinal(entry.Attempt) ||
		len(entry.PayloadJSON) > maxHeadlessChildProviderAccountingPayload || !utf8.Valid(entry.PayloadJSON) || !json.Valid(entry.PayloadJSON) {
		return ErrHeadlessChildProviderAccountingInvalidRequest
	}
	return nil
}

func cloneHeadlessChildProviderAccounting(entries []HeadlessChildProviderAccounting) []HeadlessChildProviderAccounting {
	owned := make([]HeadlessChildProviderAccounting, len(entries))
	for i, entry := range entries {
		owned[i] = entry.Clone()
	}
	return owned
}

func (s *Store) saveHeadlessChildProviderAccounting(ctx context.Context, runID string, entries []HeadlessChildProviderAccounting) error {
	// The run start has acquired the writer before any receipt read.
	for _, entry := range entries {
		previous, err := s.q.GetHeadlessChildProviderAccounting(ctx, gen.GetHeadlessChildProviderAccountingParams{
			RunID: runID, ChildTaskID: entry.ChildTaskID, Attempt: int64(entry.Attempt),
		})
		if err == nil {
			record, err := headlessChildProviderAccountingRecord(gen.HeadlessChildProviderAccounting(previous))
			if err != nil {
				return err
			}
			if record.Accounting.Version != entry.Version || !bytes.Equal(record.Accounting.PayloadJSON, entry.PayloadJSON) {
				return ErrHeadlessChildProviderAccountingConflict
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read child accounting receipt: %w", err)
		}
		if err := s.q.InsertHeadlessChildProviderAccounting(ctx, gen.InsertHeadlessChildProviderAccountingParams{
			RunID: runID, ChildTaskID: entry.ChildTaskID, Attempt: int64(entry.Attempt), Version: 1,
			PayloadJson: entry.PayloadJSON, Checksum: headlessChildProviderAccountingChecksum(runID, entry),
		}); err != nil {
			return fmt.Errorf("save child provider accounting: %w", err)
		}
	}
	return nil
}

func headlessChildProviderAccountingChecksum(runID string, entry HeadlessChildProviderAccounting) string {
	var data []byte
	for _, field := range [][]byte{
		[]byte(runID), []byte(entry.ChildTaskID),
		binary.BigEndian.AppendUint64(nil, uint64(entry.Attempt)), // #nosec G115 -- validated positive SQLite INTEGER.
		binary.BigEndian.AppendUint64(nil, entry.Version), entry.PayloadJSON,
	} {
		data = binary.BigEndian.AppendUint64(data, uint64(len(field)))
		data = append(data, field...)
	}
	return historyDigest(data)
}

func headlessChildProviderAccountingRecord(row gen.HeadlessChildProviderAccounting) (HeadlessChildProviderAccountingRecord, error) {
	if row.Version <= 0 {
		return HeadlessChildProviderAccountingRecord{}, ErrHeadlessChildProviderAccountingCorrupt
	}
	if row.Version != 1 {
		return HeadlessChildProviderAccountingRecord{}, ErrHeadlessChildProviderAccountingVersion
	}
	if row.Attempt <= 0 || uint64(row.Attempt) > uint64(math.MaxInt) {
		return HeadlessChildProviderAccountingRecord{}, ErrHeadlessChildProviderAccountingCorrupt
	}
	entry := HeadlessChildProviderAccounting{ChildTaskID: row.ChildTaskID, Attempt: int(row.Attempt), Version: uint64(row.Version), PayloadJSON: row.PayloadJson}
	if row.Sequence <= 0 || !validHeadlessAccountingRunID(row.RunID) || validateHeadlessChildProviderAccounting(entry) != nil ||
		row.Checksum != headlessChildProviderAccountingChecksum(row.RunID, entry) {
		return HeadlessChildProviderAccountingRecord{}, ErrHeadlessChildProviderAccountingCorrupt
	}
	return HeadlessChildProviderAccountingRecord{Sequence: uint64(row.Sequence), Accounting: entry.Clone()}, nil
}

// GetHeadlessChildProviderAccounting returns owned evidence for a child attempt, or ErrNotFound.
func (s *Store) GetHeadlessChildProviderAccounting(ctx context.Context, runID, childTaskID string, attempt int) (HeadlessChildProviderAccountingRecord, error) {
	if !validHeadlessAccountingRunID(runID) || childTaskID == "" || !utf8.ValidString(childTaskID) || !validHeadlessAccountingOrdinal(attempt) {
		return HeadlessChildProviderAccountingRecord{}, ErrHeadlessChildProviderAccountingInvalidRequest
	}
	row, err := s.read.GetHeadlessChildProviderAccounting(ctx, gen.GetHeadlessChildProviderAccountingParams{
		RunID: runID, ChildTaskID: childTaskID, Attempt: int64(attempt),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return HeadlessChildProviderAccountingRecord{}, ErrNotFound
	}
	if err != nil {
		return HeadlessChildProviderAccountingRecord{}, fmt.Errorf("get child provider accounting: %w", err)
	}
	return headlessChildProviderAccountingRecord(gen.HeadlessChildProviderAccounting(row))
}

// ListHeadlessChildProviderAccounting returns owned evidence strictly after an opaque cursor.
// Cursor must fit SQLite INTEGER; limit must be 1..256. Bad envelopes reject the whole page.
func (s *Store) ListHeadlessChildProviderAccounting(ctx context.Context, runID string, afterSequence uint64, limit int) ([]HeadlessChildProviderAccountingRecord, error) {
	if !validHeadlessAccountingRunID(runID) || afterSequence > math.MaxInt64 || limit < 1 || limit > 256 {
		return nil, ErrHeadlessChildProviderAccountingInvalidRequest
	}
	rows, err := s.read.ListHeadlessChildProviderAccounting(ctx, gen.ListHeadlessChildProviderAccountingParams{
		RunID: runID, AfterSequence: int64(afterSequence), PageLimit: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list child provider accounting: %w", err)
	}
	records := make([]HeadlessChildProviderAccountingRecord, len(rows))
	for i, row := range rows {
		record, err := headlessChildProviderAccountingRecord(gen.HeadlessChildProviderAccounting(row))
		if err != nil {
			return nil, err
		}
		records[i] = record
	}
	return records, nil
}
