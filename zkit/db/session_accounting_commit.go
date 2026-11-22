package db

import (
	"context"
	"fmt"
)

// CommitCompletedTurnWithAccountingVersioned atomically commits terminal session,
// transcript, history receipts, root evidence and child attempts. All existing
// transcript/version guards apply. The resulting row version is observed inside
// that same transaction and returned only after commit; every error returns zero.
// Accounting inputs are borrowed until return and independently cloned for use.
func (s *Store) CommitCompletedTurnWithAccountingVersioned(ctx context.Context, record SessionRecord, update TranscriptUpdate, expected SessionContentVersion, roots []SessionProviderAccounting, children []SessionChildProviderAccounting, history ...SessionHistoryBatch) (SessionContentVersion, error) {
	return s.CommitCompletedTurnWithAccountingBatchVersioned(ctx, record, update, expected, SessionAccountingBatch{Roots: roots, Children: children}, history...)
}

// CommitCompletedTurnWithAccountingBatchVersioned commits terminal session,
// transcript, history and all three evidence sets under the existing guards.
// Inputs are cloned before transaction I/O. The resulting row version is read
// inside that transaction and returned only after commit; every error returns zero.
func (s *Store) CommitCompletedTurnWithAccountingBatchVersioned(ctx context.Context, record SessionRecord, update TranscriptUpdate, expected SessionContentVersion, accounting SessionAccountingBatch, history ...SessionHistoryBatch) (SessionContentVersion, error) {
	if err := validateTurnAccountingBatch(record, update, accounting); err != nil {
		return SessionContentVersion{}, err
	}
	accounting = accounting.clone()
	return s.writeSessionVersioned(ctx, record.ID, expected, func(tx *Store) error {
		if err := tx.updateActiveTranscriptTx(ctx, update, &record, nil, history); err != nil {
			return err
		}
		if err := tx.saveSessionProviderAccounting(ctx, update.SessionID, accounting.Roots); err != nil {
			return err
		}
		if err := tx.saveSessionChildProviderAccounting(ctx, update.SessionID, accounting.Children); err != nil {
			return err
		}
		if err := tx.saveSessionAuxiliaryProviderAccounting(ctx, update.SessionID, accounting.Auxiliaries); err != nil {
			return err
		}
		return tx.saveSessionManualProviderAccounting(ctx, update.SessionID, accounting.Manual)
	})
}

// CommitCheckpointTurnWithAccountingVersioned retains the checkpoint-source and
// transcript guards while committing session, history and both evidence sets in
// one transaction. Its returned row version belongs to that transaction, never
// a subsequent observation. Any error rolls back all components and returns zero.
// Accounting inputs are borrowed until return and independently cloned for use.
func (s *Store) CommitCheckpointTurnWithAccountingVersioned(ctx context.Context, record SessionRecord, update TranscriptUpdate, expected SessionContentVersion, roots []SessionProviderAccounting, children []SessionChildProviderAccounting, history ...SessionHistoryBatch) (SessionContentVersion, error) {
	return s.CommitCheckpointTurnWithAccountingBatchVersioned(ctx, record, update, expected, SessionAccountingBatch{Roots: roots, Children: children}, history...)
}

// CommitCheckpointTurnWithAccountingBatchVersioned commits session, transcript,
// history and all three evidence sets with checkpoint-source and version guards.
// Inputs are cloned before transaction I/O. The resulting row version is read
// inside that transaction and returned only after commit; every error returns zero.
func (s *Store) CommitCheckpointTurnWithAccountingBatchVersioned(ctx context.Context, record SessionRecord, update TranscriptUpdate, expected SessionContentVersion, accounting SessionAccountingBatch, history ...SessionHistoryBatch) (SessionContentVersion, error) {
	if err := validateTurnAccountingBatch(record, update, accounting); err != nil {
		return SessionContentVersion{}, err
	}
	accounting = accounting.clone()
	return s.writeSessionVersioned(ctx, record.ID, expected, func(tx *Store) error {
		if err := tx.updateActiveTranscriptTx(ctx, update, &record, &expected, history); err != nil {
			return err
		}
		if err := tx.saveSessionProviderAccounting(ctx, update.SessionID, accounting.Roots); err != nil {
			return err
		}
		if err := tx.saveSessionChildProviderAccounting(ctx, update.SessionID, accounting.Children); err != nil {
			return err
		}
		if err := tx.saveSessionAuxiliaryProviderAccounting(ctx, update.SessionID, accounting.Auxiliaries); err != nil {
			return err
		}
		return tx.saveSessionManualProviderAccounting(ctx, update.SessionID, accounting.Manual)
	})
}

// CommitBeforeTurnWithAccountingVersioned atomically publishes a guarded BEFORE
// checkpoint with session, transcript, retained history and both evidence sets.
// It preserves initial-source, immutable-prefix, source and identity guards. The
// returned version is read in the publishing transaction; all errors return zero.
// Accounting inputs are borrowed until return and independently cloned for use.
func (s *Store) CommitBeforeTurnWithAccountingVersioned(ctx context.Context, record SessionRecord, update TranscriptUpdate, boundary HistoryCheckpoint, expectedActive string, expected SessionContentVersion, roots []SessionProviderAccounting, children []SessionChildProviderAccounting, history ...SessionHistoryBatch) (SessionContentVersion, error) {
	return s.CommitBeforeTurnWithAccountingBatchVersioned(ctx, record, update, boundary, expectedActive, expected, SessionAccountingBatch{Roots: roots, Children: children}, history...)
}

// CommitBeforeTurnWithAccountingBatchVersioned publishes a guarded BEFORE
// checkpoint with session, transcript, history and all three evidence sets.
// Initial-source, immutable-prefix, source and identity guards remain intact;
// expectedActive remains compatibility-only and ignored. Inputs are cloned before
// transaction I/O. The resulting version comes from that transaction only after
// commit; every error returns zero.
func (s *Store) CommitBeforeTurnWithAccountingBatchVersioned(ctx context.Context, record SessionRecord, update TranscriptUpdate, boundary HistoryCheckpoint, expectedActive string, expected SessionContentVersion, accounting SessionAccountingBatch, history ...SessionHistoryBatch) (SessionContentVersion, error) {
	if err := validateTurnAccountingBatch(record, update, accounting); err != nil {
		return SessionContentVersion{}, err
	}
	return s.commitBeforeTurnVersioned(ctx, record, update, boundary, expectedActive, expected, accounting.clone(), history)
}

func validateTurnAccounting(record SessionRecord, update TranscriptUpdate, roots []SessionProviderAccounting, children []SessionChildProviderAccounting) error {
	if err := validateProviderAccountingInput(update.SessionID, roots); err != nil {
		return err
	}
	if err := validateChildProviderAccountingInput(update.SessionID, children); err != nil {
		return err
	}
	// Child evidence may never extend an operation to a second session. Keep
	// legacy root-only guards unchanged rather than repairing unrelated APIs.
	if len(children) != 0 && record.ID != update.SessionID {
		return ErrChildProviderAccountingInvalidRequest
	}
	return nil
}

// CommitCompletedTurnWithAccounting preserves the nonversioned terminal
// transcript guards while atomically saving history and both evidence sets.
// Inputs are borrowed until return and independently cloned. A transaction-bound
// Store borrows its caller's transaction; otherwise every error rolls back.
func (s *Store) CommitCompletedTurnWithAccounting(ctx context.Context, record SessionRecord, update TranscriptUpdate, roots []SessionProviderAccounting, children []SessionChildProviderAccounting, history ...SessionHistoryBatch) error {
	return s.CommitCompletedTurnWithAccountingBatch(ctx, record, update, SessionAccountingBatch{Roots: roots, Children: children}, history...)
}

// CommitCompletedTurnWithAccountingBatch commits terminal session, transcript,
// history and all three evidence sets under existing nonversioned guards. Inputs
// are cloned before transaction I/O. A transaction-bound Store borrows its caller's
// transaction; otherwise every error rolls back all components.
func (s *Store) CommitCompletedTurnWithAccountingBatch(ctx context.Context, record SessionRecord, update TranscriptUpdate, accounting SessionAccountingBatch, history ...SessionHistoryBatch) error {
	if err := validateTurnAccountingBatch(record, update, accounting); err != nil {
		return err
	}
	return s.commitTurnWithAccounting(ctx, record, update, nil, accounting.clone(), history)
}

// CommitCheckpointTurnWithAccounting retains the nonversioned checkpoint-source
// and transcript guards while committing history and both evidence sets in one
// transaction. Inputs are borrowed until return and independently cloned. A
// transaction-bound Store borrows its caller's transaction.
func (s *Store) CommitCheckpointTurnWithAccounting(ctx context.Context, record SessionRecord, update TranscriptUpdate, expected SessionContentVersion, roots []SessionProviderAccounting, children []SessionChildProviderAccounting, history ...SessionHistoryBatch) error {
	return s.CommitCheckpointTurnWithAccountingBatch(ctx, record, update, expected, SessionAccountingBatch{Roots: roots, Children: children}, history...)
}

// CommitCheckpointTurnWithAccountingBatch commits session, transcript, history
// and all three evidence sets under existing checkpoint-source and transcript
// guards, including expected. Inputs are cloned before transaction I/O. A
// transaction-bound Store borrows its caller's transaction; otherwise errors
// roll back all components.
func (s *Store) CommitCheckpointTurnWithAccountingBatch(ctx context.Context, record SessionRecord, update TranscriptUpdate, expected SessionContentVersion, accounting SessionAccountingBatch, history ...SessionHistoryBatch) error {
	if err := validateTurnAccountingBatch(record, update, accounting); err != nil {
		return err
	}
	return s.commitTurnWithAccounting(ctx, record, update, &expected, accounting.clone(), history)
}

func (s *Store) commitTurnWithAccounting(ctx context.Context, record SessionRecord, update TranscriptUpdate, expected *SessionContentVersion, accounting SessionAccountingBatch, history []SessionHistoryBatch) error {
	commit := func(tx *Store) error {
		if len(accounting.Roots) != 0 || len(accounting.Children) != 0 || len(accounting.Auxiliaries) != 0 || len(accounting.Manual) != 0 {
			if err := tx.q.AcquireCheckpointWrite(ctx); err != nil {
				return fmt.Errorf("acquire accounting turn write: %w", err)
			}
		}
		if err := tx.updateActiveTranscriptTx(ctx, update, &record, expected, history); err != nil {
			return err
		}
		if err := tx.saveSessionProviderAccounting(ctx, update.SessionID, accounting.Roots); err != nil {
			return err
		}
		if err := tx.saveSessionChildProviderAccounting(ctx, update.SessionID, accounting.Children); err != nil {
			return err
		}
		if err := tx.saveSessionAuxiliaryProviderAccounting(ctx, update.SessionID, accounting.Auxiliaries); err != nil {
			return err
		}
		return tx.saveSessionManualProviderAccounting(ctx, update.SessionID, accounting.Manual)
	}
	if s.inTx {
		return commit(s)
	}
	return s.WithTx(ctx, commit)
}

// SessionAccountingBatch groups independent immutable root, child and auxiliary
// evidence for the same session operation. A value copy borrows slices and payloads;
// commit methods snapshot all three before blocking transaction I/O.
type SessionAccountingBatch struct {
	Roots       []SessionProviderAccounting
	Children    []SessionChildProviderAccounting
	Auxiliaries []SessionAuxiliaryProviderAccounting
	Manual      []SessionManualProviderAccounting
}

func (b SessionAccountingBatch) clone() SessionAccountingBatch {
	return SessionAccountingBatch{
		Roots:       cloneProviderAccounting(b.Roots),
		Children:    cloneChildProviderAccounting(b.Children),
		Auxiliaries: cloneAuxiliaryProviderAccounting(b.Auxiliaries),
		Manual:      cloneManualProviderAccounting(b.Manual),
	}
}

func validateTurnAccountingBatch(record SessionRecord, update TranscriptUpdate, accounting SessionAccountingBatch) error {
	if err := validateTurnAccounting(record, update, accounting.Roots, accounting.Children); err != nil {
		return err
	}
	if len(accounting.Auxiliaries) != 0 {
		if err := validateAuxiliaryProviderAccountingInput(update.SessionID, accounting.Auxiliaries); err != nil {
			return err
		}
		if record.ID != update.SessionID {
			return ErrAuxiliaryProviderAccountingInvalidRequest
		}
	}
	if len(accounting.Manual) != 0 {
		if err := validateManualProviderAccountingInput(update.SessionID, accounting.Manual); err != nil {
			return err
		}
		if record.ID != update.SessionID {
			return ErrManualProviderAccountingInvalidRequest
		}
	}
	return nil
}
