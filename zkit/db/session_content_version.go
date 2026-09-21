package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// SessionContentVersion is an opaque equality token for all stored session-row
// content. Its zero value denotes an absent row, not permission to overwrite one.
// It is not a timestamp or a monotonic revision and must not be logged.
type SessionContentVersion [sha256.Size]byte

// SessionVersion observes a session's complete stored content without returning
// private payloads. An absent row returns the zero version and no error, allowing
// callers to guard creation as well as replacement.
func (s *Store) SessionVersion(ctx context.Context, id string) (SessionContentVersion, error) {
	row, err := s.read.GetSession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionContentVersion{}, nil
	}
	if err != nil {
		return SessionContentVersion{}, fmt.Errorf("observe session content: %w", err)
	}
	// This is a process-local equality fingerprint, not a JSON wire format.
	// Include the complete generated row automatically as its schema evolves.
	data, err := json.Marshal(row) //nolint:musttag // generated storage fields intentionally define this opaque fingerprint
	if err != nil {
		return SessionContentVersion{}, errors.New("encode session content version")
	}
	return sha256.Sum256(data), nil
}

func (s *Store) checkSessionVersion(ctx context.Context, id string, expected SessionContentVersion) error {
	// All callers use a new transaction, before any other reads. Acquire the
	// SQLite writer lock first so a competing commit cannot stale our snapshot
	// between this check and the write and escape semantic conflict handling.
	if err := s.q.AcquireCheckpointWrite(ctx); err != nil {
		return fmt.Errorf("acquire checkpoint write: %w", err)
	}
	version, err := s.SessionVersion(ctx, id)
	if err != nil {
		return err
	}
	if version != expected {
		return ErrCheckpointConflict
	}
	return nil
}

// SaveSessionDraftVersioned saves a draft only while the observed session row is
// unchanged, returning its new version from the same transaction. This lets a
// serialized caller carry a source observation across its own preceding write
// without treating a competing process's content as permission to overwrite it.
func (s *Store) SaveSessionDraftVersioned(ctx context.Context, record SessionRecord, expected SessionContentVersion) (SessionContentVersion, error) {
	return s.writeSessionVersioned(ctx, record.ID, expected, func(tx *Store) error {
		return tx.SaveSessionDraft(ctx, record)
	})
}

// ClearSessionDraftVersioned clears only an unchanged draft and returns the
// committed row version. The version is zero when clearing deletes a draft-only row.
func (s *Store) ClearSessionDraftVersioned(ctx context.Context, id string, expected SessionContentVersion) (SessionContentVersion, error) {
	return s.writeSessionVersioned(ctx, id, expected, func(tx *Store) error {
		return tx.clearSessionDraft(ctx, id)
	})
}

// UpdateActiveTranscriptVersioned applies a transcript delta only while the
// complete session row matches expected, returning its version atomically.
// The transcript revision guard remains independent of the row guard.
func (s *Store) UpdateActiveTranscriptVersioned(ctx context.Context, update TranscriptUpdate, expected SessionContentVersion) (SessionContentVersion, error) {
	return s.writeSessionVersioned(ctx, update.SessionID, expected, func(tx *Store) error {
		return tx.updateActiveTranscriptTx(ctx, update, nil, nil, nil)
	})
}

// CommitCompletedTurnVersioned commits a completed turn only while its complete
// session row matches expected, returning its version from the same transaction.
// It retains CommitCompletedTurn's transcript and history guards.
func (s *Store) CommitCompletedTurnVersioned(ctx context.Context, record SessionRecord, update TranscriptUpdate, expected SessionContentVersion, history ...SessionHistoryBatch) (SessionContentVersion, error) {
	return s.writeSessionVersioned(ctx, record.ID, expected, func(tx *Store) error {
		return tx.updateActiveTranscriptTx(ctx, update, &record, nil, history)
	})
}

// CommitCheckpointTurnVersioned retains every CommitCheckpointTurn guard and
// returns the committed row version from the same transaction as the mutation.
func (s *Store) CommitCheckpointTurnVersioned(ctx context.Context, record SessionRecord, update TranscriptUpdate, expected SessionContentVersion, history ...SessionHistoryBatch) (SessionContentVersion, error) {
	return s.writeSessionVersioned(ctx, record.ID, expected, func(tx *Store) error {
		return tx.updateActiveTranscriptTx(ctx, update, &record, &expected, history)
	})
}

// DeleteSessionVersioned removes a session only if its complete row still matches
// expected. Conflict leaves both the row and its dependent history untouched.
func (s *Store) DeleteSessionVersioned(ctx context.Context, id string, expected SessionContentVersion) error {
	_, err := s.writeSessionVersioned(ctx, id, expected, func(tx *Store) error {
		return tx.DeleteSession(ctx, id)
	})
	return err
}

// RenameSessionVersioned changes only the label of an unchanged row and returns
// the committed content version so subsequent local saves retain ownership.
func (s *Store) RenameSessionVersioned(ctx context.Context, id, label string, expected SessionContentVersion) (SessionContentVersion, error) {
	return s.writeSessionVersioned(ctx, id, expected, func(tx *Store) error {
		return tx.RenameSession(ctx, id, label)
	})
}

// SaveCheckpointSourceVersioned preserves a guarded recovery head and returns
// its resulting complete row version from the same transaction.
func (s *Store) SaveCheckpointSourceVersioned(ctx context.Context, record SessionRecord, revision uint64, expected SessionContentVersion) (SessionContentVersion, error) {
	return s.writeSessionVersioned(ctx, record.ID, expected, func(tx *Store) error {
		return tx.saveCheckpointSource(ctx, record, revision, expected, nil)
	})
}

// writeSessionVersioned owns the writer lock, check, mutation and resulting
// observation. mutate must use transaction-bound methods, never start a new tx.
func (s *Store) writeSessionVersioned(ctx context.Context, id string, expected SessionContentVersion, mutate func(*Store) error) (SessionContentVersion, error) {
	var version SessionContentVersion
	err := s.WithTx(ctx, func(tx *Store) error {
		if err := tx.checkSessionVersion(ctx, id, expected); err != nil {
			return err
		}
		if err := mutate(tx); err != nil {
			return err
		}
		var err error
		version, err = tx.SessionVersion(ctx, id)
		return err
	})
	if err != nil {
		return SessionContentVersion{}, err
	}
	return version, nil
}
