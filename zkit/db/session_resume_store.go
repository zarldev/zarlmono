package db

import (
	"context"
	"errors"
)

// SessionResumeState owns one transactionally consistent saved session and its
// integrity-checked canonical transcript and branch provenance. Transcript is
// absent for draft-only sessions; a present revision-zero transcript is distinct
// from that absence. Branch is absent for original sessions.
type SessionResumeState struct {
	Session        SessionRecord
	Transcript     *SessionTranscript
	Branch         *SessionBranch
	ContentVersion SessionContentVersion // complete row observed in the same read transaction
}

// GetSessionResumeState reads context, draft, and canonical history in the same
// transaction so a concurrent completed-turn commit cannot tear a resume. It
// does not establish runtime quiescence or keep the returned head current.
func (s *Store) GetSessionResumeState(ctx context.Context, id string) (SessionResumeState, error) {
	var state SessionResumeState
	err := s.withReadTx(ctx, func(tx *Store) error {
		record, err := tx.GetSession(ctx, id)
		if err != nil {
			return err
		}
		thread, err := tx.getSessionTranscript(ctx, id)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		state.Session = record
		if err == nil {
			state.Transcript = &thread
		}
		branch, err := tx.GetSessionBranch(ctx, id)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if err == nil {
			state.Branch = &branch
		}
		state.ContentVersion, err = tx.SessionVersion(ctx, id)
		return err
	})
	if err != nil {
		return SessionResumeState{}, err
	}
	return state, nil
}
