package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/zarldev/zarlmono/zkit/db/gen"
)

// ErrTranscriptConflict means another writer advanced the canonical thread.
var ErrTranscriptConflict = errors.New("transcript revision conflict")

// ErrTranscriptCorrupt means persisted canonical thread rows failed integrity verification.
var ErrTranscriptCorrupt = errors.New("session transcript is corrupted")

// SessionTranscriptFormatVersion is the current canonical payload contract.
const SessionTranscriptFormatVersion uint64 = 2

// TranscriptEntry is one ordered renderer-independent persistence record.
type TranscriptEntry struct {
	Sequence    uint64
	EntryID     string
	ParentID    string
	TurnID      string
	Kind        string
	PayloadJSON []byte
	Revision    uint64
}

// SessionTranscript is the durable canonical thread for a saved session.
type SessionTranscript struct {
	SessionID     string
	Revision      uint64
	Checksum      string
	FormatVersion uint64
	Entries       []TranscriptEntry
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// TranscriptUpdate is one optimistic incremental canonical-thread write.
type TranscriptUpdate struct {
	SessionID        string
	Workspace        string
	Label            string
	LabelManual      bool
	AgentName        string
	Provider         string
	Model            string
	MessageCount     int
	CreatedAt        time.Time
	ExpectedRevision uint64
	Revision         uint64
	Entries          []TranscriptEntry
}

// GetSessionTranscript returns the ordered canonical thread for sessionID.
func (s *Store) GetSessionTranscript(ctx context.Context, sessionID string) (SessionTranscript, error) {
	var transcript SessionTranscript
	err := s.WithTx(ctx, func(tx *Store) error {
		var getErr error
		transcript, getErr = tx.getSessionTranscript(ctx, sessionID)
		return getErr
	})
	if err != nil {
		return SessionTranscript{}, err
	}
	return transcript, nil
}

func (s *Store) getSessionTranscript(ctx context.Context, sessionID string) (SessionTranscript, error) {
	metadata, err := s.q.GetSessionTranscript(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionTranscript{}, ErrNotFound
	}
	if err != nil {
		return SessionTranscript{}, fmt.Errorf("get session transcript: %w", err)
	}
	rows, err := s.q.ListSessionTranscriptEntries(ctx, sessionID)
	if err != nil {
		return SessionTranscript{}, fmt.Errorf("list session transcript entries: %w", err)
	}
	revision, err := nonnegativeUint64("transcript revision", metadata.Revision)
	if err != nil {
		return SessionTranscript{}, fmt.Errorf("%w: %w", ErrTranscriptCorrupt, err)
	}
	formatVersion, err := nonnegativeUint64("transcript format version", metadata.FormatVersion)
	if err != nil || formatVersion == 0 {
		if err == nil {
			err = errors.New("transcript format version is zero")
		}
		return SessionTranscript{}, fmt.Errorf("%w: %w", ErrTranscriptCorrupt, err)
	}
	entries := make([]TranscriptEntry, len(rows))
	for i, row := range rows {
		sequence, conversionErr := nonnegativeUint64("entry sequence", row.Sequence)
		if conversionErr != nil {
			return SessionTranscript{}, fmt.Errorf("%w: %w", ErrTranscriptCorrupt, conversionErr)
		}
		entryRevision, conversionErr := nonnegativeUint64("entry revision", row.Revision)
		if conversionErr != nil {
			return SessionTranscript{}, fmt.Errorf("%w: %w", ErrTranscriptCorrupt, conversionErr)
		}
		entries[i] = TranscriptEntry{
			Sequence: sequence, EntryID: row.EntryID, ParentID: row.ParentID,
			TurnID: row.TurnID, Kind: row.Kind, PayloadJSON: []byte(row.PayloadJson), Revision: entryRevision,
		}
	}
	expected := transcriptChecksum(metadata.SessionID, revision, entries)
	if metadata.Checksum != expected {
		return SessionTranscript{}, fmt.Errorf("%w: checksum mismatch", ErrTranscriptCorrupt)
	}
	return SessionTranscript{
		SessionID: metadata.SessionID, Revision: revision, Checksum: metadata.Checksum,
		FormatVersion: formatVersion, Entries: entries,
		CreatedAt: time.UnixMilli(metadata.CreatedAtMs), UpdatedAt: time.UnixMilli(metadata.UpdatedAtMs),
	}, nil
}

// UpdateActiveTranscript atomically applies changed entries when the durable
// revision equals ExpectedRevision.
func (s *Store) UpdateActiveTranscript(ctx context.Context, update TranscriptUpdate) error {
	return s.updateActiveTranscript(ctx, update, nil)
}

// CommitCompletedTurn atomically saves terminal model context and advances the
// canonical transcript revision.
func (s *Store) CommitCompletedTurn(ctx context.Context, record SessionRecord, update TranscriptUpdate) error {
	return s.updateActiveTranscript(ctx, update, &record)
}

func (s *Store) updateActiveTranscript(ctx context.Context, update TranscriptUpdate, record *SessionRecord) error {
	if update.SessionID == "" {
		return errors.New("update transcript: session ID is empty")
	}
	if update.Workspace == "" {
		return fmt.Errorf("update transcript %q: workspace is empty", update.SessionID)
	}
	if record != nil && record.Workspace != update.Workspace {
		return fmt.Errorf("update transcript %q: record workspace %q does not match transcript workspace %q", update.SessionID, record.Workspace, update.Workspace)
	}
	if record != nil && update.Revision == update.ExpectedRevision {
		if len(update.Entries) != 0 {
			return fmt.Errorf("update transcript %q: %d pending entries at terminal revision %d", update.SessionID, len(update.Entries), update.Revision)
		}
		return s.commitCompletedSessionOnly(ctx, *record, update)
	}
	if update.Revision < update.ExpectedRevision {
		return fmt.Errorf("update transcript %q: revision %d before expected %d", update.SessionID, update.Revision, update.ExpectedRevision)
	}
	if update.Revision == update.ExpectedRevision {
		return fmt.Errorf("update transcript %q: revision %d does not advance expected %d", update.SessionID, update.Revision, update.ExpectedRevision)
	}
	revision, err := sqliteInteger("transcript revision", update.Revision)
	if err != nil {
		return fmt.Errorf("update transcript %q: %w", update.SessionID, err)
	}
	expectedRevision, err := sqliteInteger("expected transcript revision", update.ExpectedRevision)
	if err != nil {
		return fmt.Errorf("update transcript %q: %w", update.SessionID, err)
	}
	entries := make([]gen.UpsertSessionTranscriptEntryParams, len(update.Entries))
	for i, entry := range update.Entries {
		sequence, conversionErr := sqliteInteger("transcript entry sequence", entry.Sequence)
		if conversionErr != nil {
			return fmt.Errorf("update transcript %q entry %q: %w", update.SessionID, entry.EntryID, conversionErr)
		}
		entryRevision, conversionErr := sqliteInteger("transcript entry revision", entry.Revision)
		if conversionErr != nil {
			return fmt.Errorf("update transcript %q entry %q: %w", update.SessionID, entry.EntryID, conversionErr)
		}
		entries[i] = gen.UpsertSessionTranscriptEntryParams{
			SessionID: update.SessionID, Sequence: sequence, EntryID: entry.EntryID,
			ParentID: entry.ParentID, TurnID: entry.TurnID, Kind: entry.Kind,
			PayloadJson: string(entry.PayloadJSON), Revision: entryRevision,
		}
	}
	if err := validateTranscriptDelta(update, entries); err != nil {
		return fmt.Errorf("update transcript %q: %w", update.SessionID, err)
	}
	now := time.Now()
	if update.CreatedAt.IsZero() {
		update.CreatedAt = now
	}
	if err := s.WithTx(ctx, func(tx *Store) error {
		if err := tx.ensureSessionWorkspace(ctx, update.SessionID, update.Workspace); err != nil {
			return err
		}
		if record != nil {
			if err := tx.SaveSession(ctx, *record); err != nil {
				return err
			}
		} else if err := tx.q.UpsertSessionTranscriptMetadata(ctx, gen.UpsertSessionTranscriptMetadataParams{
			ID: update.SessionID, Workspace: update.Workspace, Label: update.Label,
			LabelManual: boolToInt64(update.LabelManual), AgentName: update.AgentName,
			Provider: update.Provider, Model: update.Model, MessageCount: int64(update.MessageCount),
			CreatedAt: update.CreatedAt.Unix(), UpdatedAt: now.Unix(),
		}); err != nil {
			return fmt.Errorf("update transcript metadata: %w", err)
		}
		if err := tx.q.EnsureSessionTranscript(ctx, gen.EnsureSessionTranscriptParams{
			SessionID: update.SessionID, Checksum: transcriptChecksum(update.SessionID, 0, nil),
			FormatVersion: int64(SessionTranscriptFormatVersion), CreatedAtMs: update.CreatedAt.UnixMilli(), UpdatedAtMs: now.UnixMilli(),
		}); err != nil {
			return fmt.Errorf("ensure session transcript: %w", err)
		}
		for _, entry := range entries {
			if err := tx.q.UpsertSessionTranscriptEntry(ctx, entry); err != nil {
				return fmt.Errorf("upsert transcript entry %q: %w", entry.EntryID, err)
			}
		}
		allRows, err := tx.q.ListSessionTranscriptEntries(ctx, update.SessionID)
		if err != nil {
			return fmt.Errorf("list transcript entries for checksum: %w", err)
		}
		allEntries := make([]TranscriptEntry, len(allRows))
		for i, row := range allRows {
			sequence, conversionErr := nonnegativeUint64("entry sequence", row.Sequence)
			if conversionErr != nil {
				return fmt.Errorf("read transcript entry %q: %w", row.EntryID, conversionErr)
			}
			entryRevision, conversionErr := nonnegativeUint64("entry revision", row.Revision)
			if conversionErr != nil {
				return fmt.Errorf("read transcript entry %q: %w", row.EntryID, conversionErr)
			}
			allEntries[i] = TranscriptEntry{
				Sequence: sequence, EntryID: row.EntryID, ParentID: row.ParentID,
				TurnID: row.TurnID, Kind: row.Kind, PayloadJSON: []byte(row.PayloadJson), Revision: entryRevision,
			}
		}
		if err := validateStoredTranscript(update, allEntries); err != nil {
			return err
		}
		checksum := transcriptChecksum(update.SessionID, update.Revision, allEntries)
		result, err := tx.q.AdvanceSessionTranscript(ctx, gen.AdvanceSessionTranscriptParams{
			Revision: revision, Checksum: checksum, FormatVersion: int64(SessionTranscriptFormatVersion), UpdatedAtMs: now.UnixMilli(),
			SessionID: update.SessionID, ExpectedRevision: expectedRevision,
		})
		if err != nil {
			return fmt.Errorf("advance transcript revision: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read transcript revision result: %w", err)
		}
		if changed != 1 {
			return ErrTranscriptConflict
		}
		if err := tx.SetSetting(ctx, update.Workspace, activeSessionSettingKey, update.SessionID); err != nil {
			return fmt.Errorf("mark session %q active: %w", update.SessionID, err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("update active transcript %q: %w", update.SessionID, err)
	}
	return nil
}

// commitCompletedSessionOnly persists a terminal session snapshot whose
// canonical transcript is already durable. It commits the resumable model
// context and workspace state without advancing the transcript revision.
func (s *Store) commitCompletedSessionOnly(ctx context.Context, record SessionRecord, update TranscriptUpdate) error {
	if err := s.SaveActiveSession(ctx, record); err != nil {
		return fmt.Errorf("commit completed turn %q: %w", update.SessionID, err)
	}
	return nil
}
func validateTranscriptDelta(update TranscriptUpdate, entries []gen.UpsertSessionTranscriptEntryParams) error {
	if len(entries) == 0 {
		return errors.New("transcript update has no changed entries")
	}
	for _, entry := range entries {
		if entry.Sequence <= 0 {
			return fmt.Errorf("entry %q has invalid sequence %d", entry.EntryID, entry.Sequence)
		}
		if entry.EntryID == "" {
			return errors.New("transcript entry ID is empty")
		}
		if entry.Kind == "" {
			return fmt.Errorf("entry %q kind is empty", entry.EntryID)
		}
		if entry.Revision <= 0 || uint64(entry.Revision) > update.Revision {
			return fmt.Errorf("entry %q has invalid revision %d for transcript revision %d", entry.EntryID, entry.Revision, update.Revision)
		}
	}
	return nil
}

func validateStoredTranscript(update TranscriptUpdate, entries []TranscriptEntry) error {
	if len(entries) == 0 {
		return errors.New("transcript has no entries")
	}
	var highestRevision uint64
	for i, entry := range entries {
		if entry.Sequence != uint64(i+1) {
			return fmt.Errorf("transcript sequence %d at position %d", entry.Sequence, i+1)
		}
		if entry.Revision == 0 || entry.Revision > update.Revision {
			return fmt.Errorf("transcript entry %q has invalid revision %d for transcript revision %d", entry.EntryID, entry.Revision, update.Revision)
		}
		if entry.Revision > highestRevision {
			highestRevision = entry.Revision
		}
		if entry.EntryID == "" {
			return errors.New("transcript entry ID is empty")
		}
		if entry.Kind == "" {
			return fmt.Errorf("transcript entry %q kind is empty", entry.EntryID)
		}
		if !json.Valid(entry.PayloadJSON) {
			return fmt.Errorf("transcript entry %q payload is invalid JSON", entry.EntryID)
		}
	}
	if highestRevision != update.Revision {
		return fmt.Errorf("highest entry revision %d does not match transcript revision %d", highestRevision, update.Revision)
	}
	return nil
}

func nonnegativeUint64(name string, value int64) (uint64, error) {
	if value < 0 {
		return 0, fmt.Errorf("%s %d is negative", name, value)
	}
	return uint64(value), nil
}

func sqliteInteger(name string, value uint64) (int64, error) {
	if value > math.MaxInt64 {
		return 0, fmt.Errorf("%s %d exceeds SQLite INTEGER", name, value)
	}
	return int64(value), nil
}
