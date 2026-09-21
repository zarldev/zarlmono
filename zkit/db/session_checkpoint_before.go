package db

import (
	"context"
	"errors"
	"time"

	"github.com/zarldev/zarlmono/zkit/db/gen"
)

// HistoryCheckpoint is a prepared immutable boundary, without a published replay
// head. State is opaque application metadata; Checkpoint.History.StateID must be
// its SHA-256 hex digest. Entries and State are borrowed for the commit only.
type HistoryCheckpoint struct {
	Checkpoint SessionCheckpoint
	Entries    []TranscriptEntry
	State      []byte
}

// CommitBeforeTurn atomically publishes a guarded recovery row, transcript,
// retained replay/request batches and a BEFORE checkpoint. An absent transcript
// may only be promoted from an empty initial session. Existing sessions must
// match both observed content and revision. expectedActive is retained for API
// compatibility but ignored; workspace selection is not source ownership.
// A failed publication rolls everything back; callers acknowledge batches only
// after success. Reusing a checkpoint identity conflicts rather than dispatching
// again after an ambiguous commit.
func (s *Store) CommitBeforeTurn(ctx context.Context, record SessionRecord, update TranscriptUpdate, boundary HistoryCheckpoint, expectedActive string, expected SessionContentVersion, history ...SessionHistoryBatch) error {
	_, err := s.CommitBeforeTurnVersioned(ctx, record, update, boundary, expectedActive, expected, history...)
	return err
}

// CommitBeforeTurnVersioned retains every BEFORE publication guard and returns
// the resulting row version from that transaction, only after it commits.
func (s *Store) CommitBeforeTurnVersioned(ctx context.Context, record SessionRecord, update TranscriptUpdate, boundary HistoryCheckpoint, expectedActive string, expected SessionContentVersion, history ...SessionHistoryBatch) (SessionContentVersion, error) {
	checkpoint := boundary.Checkpoint
	if len(checkpoint.Payload) > CheckpointPayloadLimit {
		return SessionContentVersion{}, ErrCheckpointQuota
	}
	if !checkpointMetadataValid(checkpoint) || checkpoint.SessionID != record.ID || checkpoint.SourceSessionID != record.ID ||
		checkpoint.Workspace != record.Workspace || update.SessionID != record.ID || update.Workspace != record.Workspace ||
		checkpoint.SourceRevision != update.Revision || checkpoint.History.StateID != historyDigest(boundary.State) {
		return SessionContentVersion{}, ErrCheckpointCorrupt
	}
	checkpoint.CreatedAt = time.Now().Truncate(time.Millisecond)
	var version SessionContentVersion
	err := s.WithTx(ctx, func(tx *Store) error {
		if err := tx.checkSessionVersion(ctx, record.ID, expected); err != nil {
			return err
		}
		stored, err := tx.getSessionTranscript(ctx, record.ID)
		if errors.Is(err, ErrNotFound) && update.Revision == 0 && update.ExpectedRevision == 0 && len(update.Entries) == 0 && len(boundary.Entries) == 0 {
			if err := tx.establishInitialCheckpointSource(ctx, record, checkpoint, expectedActive, expected); err != nil {
				return err
			}
			if err := tx.q.SetHistoryHead(ctx, gen.SetHistoryHeadParams{SessionID: record.ID, Kind: replayHistoryKind, Head: ""}); err != nil {
				return err
			}
			if err := tx.saveSessionHistory(ctx, record.ID, history); err != nil {
				return err
			}
		} else {
			if err != nil {
				return err
			}
			if stored.Revision != update.ExpectedRevision {
				return ErrTranscriptConflict
			}
			if err := tx.updateActiveTranscriptTx(ctx, update, &record, &expected, history); err != nil {
				return err
			}
		}
		// Verify that the immutable display prefix is the complete committed
		// transcript, not a caller-selected partial or stale representation.
		stored, err = tx.getSessionTranscript(ctx, record.ID)
		if err != nil {
			return err
		}
		if stored.Checksum != transcriptChecksum(record.ID, update.Revision, boundary.Entries) {
			return ErrTranscriptConflict
		}
		checkpoint.History, err = tx.captureSessionHistory(ctx, record.ID, boundary.Entries, boundary.State)
		if err != nil {
			return err
		}
		checkpoint.Checksum = checkpointChecksum(checkpoint)
		if err := tx.saveSessionCheckpoint(ctx, checkpoint); err != nil {
			return err
		}
		version, err = tx.SessionVersion(ctx, record.ID)
		return err
	})
	if err != nil {
		return SessionContentVersion{}, err
	}
	return version, nil
}
