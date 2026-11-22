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

// ErrHeadlessProviderAccountingInvalidRequest means scope, identity, payload or pagination is invalid.
var ErrHeadlessProviderAccountingInvalidRequest = errors.New("invalid headless provider accounting request")

// ErrHeadlessProviderAccountingConflict means a frozen start or occurrence already has different evidence.
var ErrHeadlessProviderAccountingConflict = errors.New("headless provider accounting conflict")

// ErrHeadlessProviderAccountingCorrupt means stored evidence failed envelope integrity checks.
var ErrHeadlessProviderAccountingCorrupt = errors.New("headless provider accounting is corrupted")

// ErrHeadlessProviderAccountingVersion means the envelope version is unsupported.
var ErrHeadlessProviderAccountingVersion = errors.New("unsupported headless provider accounting version")

const maxHeadlessProviderAccountingPayload = 1 << 20

// HeadlessProviderAccounting is immutable evidence for one one-based provider
// occurrence within a one-based pursue execution. Version 1 accepts opaque valid
// UTF-8 JSON up to 1 MiB; application payload semantics belong to the caller.
// Value copies borrow PayloadJSON; Clone owns its bytes.
type HeadlessProviderAccounting struct {
	PursueAttempt   int
	ProviderOrdinal int
	Version         uint64
	PayloadJSON     []byte
}

// Clone returns an envelope with independent payload bytes.
func (r HeadlessProviderAccounting) Clone() HeadlessProviderAccounting {
	r.PayloadJSON = slices.Clone(r.PayloadJSON)
	return r
}

// HeadlessProviderAccountingRecord pairs evidence with an increasing opaque
// cursor, not a contiguous count or a per-run ordinal.
type HeadlessProviderAccountingRecord struct {
	Sequence   uint64
	Accounting HeadlessProviderAccounting
}

func validHeadlessAccountingRunID(runID string) bool {
	return runID != "" && utf8.ValidString(runID)
}

func validHeadlessAccountingOrdinal(n int) bool {
	return n > 0 && uint64(n) <= math.MaxInt64
}

func validateHeadlessProviderAccounting(r HeadlessProviderAccounting) error {
	if r.Version != 1 {
		return ErrHeadlessProviderAccountingVersion
	}
	if !validHeadlessAccountingOrdinal(r.PursueAttempt) || !validHeadlessAccountingOrdinal(r.ProviderOrdinal) ||
		len(r.PayloadJSON) > maxHeadlessProviderAccountingPayload || !utf8.Valid(r.PayloadJSON) || !json.Valid(r.PayloadJSON) {
		return ErrHeadlessProviderAccountingInvalidRequest
	}
	return nil
}

func cloneHeadlessAccountingNumber(n *int64) *int64 {
	if n == nil {
		return nil
	}
	v := *n
	return &v
}

// CommitHeadlessRecording atomically ensures an immutable start, upserts included
// legacy attempt summaries, saves immutable provider occurrences, and optionally
// completes the run. Identical occurrences retain their original cursor. Start
// identity compares persisted Unix seconds, ignoring mutable progress/summary.
// Included aliases are snapshotted before blocking I/O. Callers must not mutate
// inputs during this call. A transaction-bound Store borrows its enclosing tx;
// the enclosing owner must roll back on any error. Verifier results are excluded.
func (s *Store) CommitHeadlessRecording(ctx context.Context, start HeadlessRunStart, attempts []HeadlessAttemptRecord, accounting []HeadlessProviderAccounting, completion *HeadlessRunSummary) error {
	return s.CommitHeadlessRecordingWithAccountingBatch(ctx, start, attempts, HeadlessAccountingBatch{Providers: accounting}, completion)
}

// HeadlessAccountingBatch groups immutable main, auxiliary, and child stream evidence.
// Value copies borrow slices and payloads; the commit snapshots them before I/O.
type HeadlessAccountingBatch struct {
	Providers   []HeadlessProviderAccounting
	Auxiliaries []HeadlessAuxiliaryProviderAccounting
	Children    []HeadlessChildProviderAccounting
}

// CommitHeadlessRecordingWithAccountingBatch atomically ensures a frozen run start,
// upserts attempts, saves main, auxiliary, and child evidence, and optionally
// completes the run. Identical evidence retains its original cursor. Inputs are snapshotted
// before blocking I/O; callers must not mutate inputs during the call. A
// transaction-bound Store borrows its enclosing transaction, whose owner must
// roll back on any error. Start identity compares Unix seconds, ignoring progress.
func (s *Store) CommitHeadlessRecordingWithAccountingBatch(ctx context.Context, start HeadlessRunStart, attempts []HeadlessAttemptRecord, accounting HeadlessAccountingBatch, completion *HeadlessRunSummary) error {
	ownedAttempts := slices.Clone(attempts)
	for i, attempt := range ownedAttempts {
		attempt.TokensIn = cloneHeadlessAccountingNumber(attempt.TokensIn)
		attempt.TokensOut = cloneHeadlessAccountingNumber(attempt.TokensOut)
		ownedAttempts[i] = attempt
	}
	ownedAccounting := make([]HeadlessProviderAccounting, len(accounting.Providers))
	for i, entry := range accounting.Providers {
		ownedAccounting[i] = entry.Clone()
	}
	ownedAuxiliaries := cloneHeadlessAuxiliaryProviderAccounting(accounting.Auxiliaries)
	ownedChildren := cloneHeadlessChildProviderAccounting(accounting.Children)
	if completion != nil {
		summary := *completion
		summary.TokensIn = cloneHeadlessAccountingNumber(summary.TokensIn)
		summary.TokensOut = cloneHeadlessAccountingNumber(summary.TokensOut)
		completion = &summary
	}
	if !validHeadlessAccountingRunID(start.ID) {
		return ErrHeadlessProviderAccountingInvalidRequest
	}
	for _, attempt := range ownedAttempts {
		if attempt.RunID != start.ID || !validHeadlessAccountingOrdinal(attempt.AttemptNumber) {
			return ErrHeadlessProviderAccountingInvalidRequest
		}
	}
	for _, entry := range ownedAccounting {
		if err := validateHeadlessProviderAccounting(entry); err != nil {
			return err
		}
	}
	for _, entry := range ownedAuxiliaries {
		if err := validateHeadlessAuxiliaryProviderAccounting(entry); err != nil {
			return err
		}
	}
	for _, entry := range ownedChildren {
		if err := validateHeadlessChildProviderAccounting(entry); err != nil {
			return err
		}
	}
	if s.inTx {
		return s.commitHeadlessRecording(ctx, start, ownedAttempts, ownedAccounting, ownedAuxiliaries, ownedChildren, completion)
	}
	return s.WithTx(ctx, func(tx *Store) error {
		return tx.commitHeadlessRecording(ctx, start, ownedAttempts, ownedAccounting, ownedAuxiliaries, ownedChildren, completion)
	})
}

func (s *Store) commitHeadlessRecording(ctx context.Context, start HeadlessRunStart, attempts []HeadlessAttemptRecord, accounting []HeadlessProviderAccounting, auxiliaries []HeadlessAuxiliaryProviderAccounting, children []HeadlessChildProviderAccounting, completion *HeadlessRunSummary) error {
	// This write must precede every read, even on retries with no new evidence:
	// independently opened Stores must serialize before taking their read snapshot.
	if err := s.q.EnsureHeadlessRecordingStart(ctx, gen.EnsureHeadlessRecordingStartParams{
		ID: start.ID, Workspace: start.Workspace, BaseCommit: nullableString(start.BaseCommit),
		Prompt: start.Prompt, StartedAt: start.StartedAt.Unix(), Provider: start.Provider, Model: start.Model,
	}); err != nil {
		return fmt.Errorf("ensure headless recording start: %w", err)
	}
	previous, err := s.GetHeadlessRun(ctx, start.ID)
	if err != nil {
		return err
	}
	if previous.ID != start.ID || previous.Workspace != start.Workspace || previous.BaseCommit != start.BaseCommit ||
		previous.Prompt != start.Prompt || previous.StartedAt.Unix() != start.StartedAt.Unix() ||
		previous.Provider != start.Provider || previous.Model != start.Model {
		return ErrHeadlessProviderAccountingConflict
	}
	for _, attempt := range attempts {
		if err := s.InsertHeadlessAttempt(ctx, attempt); err != nil {
			return err
		}
	}
	for _, entry := range accounting {
		row, err := s.q.GetHeadlessProviderAccounting(ctx, gen.GetHeadlessProviderAccountingParams{
			RunID: start.ID, PursueAttempt: int64(entry.PursueAttempt), ProviderOrdinal: int64(entry.ProviderOrdinal),
		})
		if err == nil {
			record, err := headlessProviderAccountingRecord(gen.HeadlessProviderAccounting(row))
			if err != nil {
				return err
			}
			if record.Accounting.Version != entry.Version || !bytes.Equal(record.Accounting.PayloadJSON, entry.PayloadJSON) {
				return ErrHeadlessProviderAccountingConflict
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read headless accounting receipt: %w", err)
		}
		if err := s.q.InsertHeadlessProviderAccounting(ctx, gen.InsertHeadlessProviderAccountingParams{
			RunID: start.ID, PursueAttempt: int64(entry.PursueAttempt), ProviderOrdinal: int64(entry.ProviderOrdinal),
			Version: 1, PayloadJson: entry.PayloadJSON, Checksum: headlessProviderAccountingChecksum(start.ID, entry),
		}); err != nil {
			return fmt.Errorf("save headless provider accounting: %w", err)
		}
	}
	if err := s.saveHeadlessAuxiliaryProviderAccounting(ctx, start.ID, auxiliaries); err != nil {
		return err
	}
	if err := s.saveHeadlessChildProviderAccounting(ctx, start.ID, children); err != nil {
		return err
	}
	if completion != nil {
		return s.CompleteHeadlessRun(ctx, start.ID, *completion)
	}
	return nil
}

func headlessProviderAccountingChecksum(runID string, entry HeadlessProviderAccounting) string {
	var data []byte
	for _, field := range [][]byte{
		[]byte(runID),
		binary.BigEndian.AppendUint64(nil, uint64(entry.PursueAttempt)),   // #nosec G115 -- validated positive SQLite INTEGER.
		binary.BigEndian.AppendUint64(nil, uint64(entry.ProviderOrdinal)), // #nosec G115 -- validated positive SQLite INTEGER.
		binary.BigEndian.AppendUint64(nil, entry.Version), entry.PayloadJSON,
	} {
		data = binary.BigEndian.AppendUint64(data, uint64(len(field)))
		data = append(data, field...)
	}
	return historyDigest(data)
}

func headlessProviderAccountingRecord(row gen.HeadlessProviderAccounting) (HeadlessProviderAccountingRecord, error) {
	if row.Version <= 0 {
		return HeadlessProviderAccountingRecord{}, ErrHeadlessProviderAccountingCorrupt
	}
	if row.Version != 1 {
		return HeadlessProviderAccountingRecord{}, ErrHeadlessProviderAccountingVersion
	}
	if row.PursueAttempt <= 0 || uint64(row.PursueAttempt) > uint64(math.MaxInt) ||
		row.ProviderOrdinal <= 0 || uint64(row.ProviderOrdinal) > uint64(math.MaxInt) {
		return HeadlessProviderAccountingRecord{}, ErrHeadlessProviderAccountingCorrupt
	}
	entry := HeadlessProviderAccounting{
		PursueAttempt: int(row.PursueAttempt), ProviderOrdinal: int(row.ProviderOrdinal), Version: uint64(row.Version), PayloadJSON: row.PayloadJson,
	}
	if row.Sequence <= 0 || !validHeadlessAccountingRunID(row.RunID) || validateHeadlessProviderAccounting(entry) != nil ||
		row.Checksum != headlessProviderAccountingChecksum(row.RunID, entry) {
		return HeadlessProviderAccountingRecord{}, ErrHeadlessProviderAccountingCorrupt
	}
	return HeadlessProviderAccountingRecord{Sequence: uint64(row.Sequence), Accounting: entry.Clone()}, nil
}

// GetHeadlessProviderAccounting returns owned evidence for the scoped occurrence,
// or ErrNotFound. Corrupt or unsupported envelopes are never repaired.
func (s *Store) GetHeadlessProviderAccounting(ctx context.Context, runID string, pursueAttempt, providerOrdinal int) (HeadlessProviderAccountingRecord, error) {
	if !validHeadlessAccountingRunID(runID) || !validHeadlessAccountingOrdinal(pursueAttempt) || !validHeadlessAccountingOrdinal(providerOrdinal) {
		return HeadlessProviderAccountingRecord{}, ErrHeadlessProviderAccountingInvalidRequest
	}
	row, err := s.read.GetHeadlessProviderAccounting(ctx, gen.GetHeadlessProviderAccountingParams{
		RunID: runID, PursueAttempt: int64(pursueAttempt), ProviderOrdinal: int64(providerOrdinal),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return HeadlessProviderAccountingRecord{}, ErrNotFound
	}
	if err != nil {
		return HeadlessProviderAccountingRecord{}, fmt.Errorf("get headless provider accounting: %w", err)
	}
	return headlessProviderAccountingRecord(gen.HeadlessProviderAccounting(row))
}

// ListHeadlessProviderAccounting returns owned evidence strictly after an opaque
// sequence cursor fitting SQLite INTEGER, with limit 1..256. Any corrupt or
// unsupported envelope rejects the entire page.
func (s *Store) ListHeadlessProviderAccounting(ctx context.Context, runID string, afterSequence uint64, limit int) ([]HeadlessProviderAccountingRecord, error) {
	if !validHeadlessAccountingRunID(runID) || afterSequence > math.MaxInt64 || limit < 1 || limit > 256 {
		return nil, ErrHeadlessProviderAccountingInvalidRequest
	}
	rows, err := s.read.ListHeadlessProviderAccounting(ctx, gen.ListHeadlessProviderAccountingParams{
		RunID: runID, AfterSequence: int64(afterSequence), PageLimit: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list headless provider accounting: %w", err)
	}
	records := make([]HeadlessProviderAccountingRecord, len(rows))
	for i, row := range rows {
		record, err := headlessProviderAccountingRecord(gen.HeadlessProviderAccounting(row))
		if err != nil {
			return nil, err
		}
		records[i] = record
	}
	return records, nil
}
