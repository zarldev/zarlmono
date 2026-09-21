package tui

import (
	"context"
	"slices"
	"sync/atomic"

	"github.com/zarldev/zarlmono/zkit/db"
)

const sourceConflictNotice = "session changed elsewhere — copy your input, then reload the session or start a new conversation; local saves paused"

// Only a committed, atomic local write can advance an observation. Rereading
// the current row at execution would silently adopt another process's changes.
type sourceWrite struct {
	before db.SessionContentVersion
	after  db.SessionContentVersion
}

type sourceObservation struct {
	version   db.SessionContentVersion
	preceding []*atomic.Pointer[sourceWrite]
}

func (m *UI) observeSource(ctx context.Context, sessionID string) (sourceObservation, error) {
	if err := ctx.Err(); err != nil {
		return sourceObservation{}, err
	}
	return m.withPendingSource(sourceObservation{version: m.sourceBaseline}, sessionID), nil
}

func (m *UI) withPendingSource(observation sourceObservation, sessionID string) sourceObservation {
	observation.preceding = append([]*atomic.Pointer[sourceWrite](nil), observation.preceding...)
	if op := m.sessionPersistCurrent; op != nil && op.sessionID() == sessionID && op.sourceWritten != nil && !slices.Contains(observation.preceding, op.sourceWritten) {
		observation.preceding = append(observation.preceding, op.sourceWritten)
	}
	for _, op := range m.sessionPersistQueue {
		if op.sessionID() == sessionID && op.sourceWritten != nil && !slices.Contains(observation.preceding, op.sourceWritten) {
			observation.preceding = append(observation.preceding, op.sourceWritten)
		}
	}
	return observation
}

func (s sourceObservation) expected() db.SessionContentVersion {
	version := s.version
	for _, receipt := range s.preceding {
		if write := receipt.Load(); write != nil && write.before == version {
			version = write.after
		}
	}
	return version
}

func (m *UI) acknowledgeSource(op *sessionPersistOp) {
	if op.sessionID() != m.session.ID || op.sourceWritten == nil {
		return
	}
	write := op.sourceWritten.Load()
	if write == nil {
		return
	}
	if write.before == m.sourceBaseline {
		m.sourceBaseline = write.after
	}
	// A failed settlement retains its original observation after the FIFO
	// releases it. Carry later acknowledged local writes into a retry without
	// adopting current database state or duplicating an already-held receipt.
	if b := m.completedBoundary; b != nil && b.sessionID == op.sessionID() && b.source.expected() == write.before && !slices.Contains(b.source.preceding, op.sourceWritten) {
		b.source.preceding = append(append([]*atomic.Pointer[sourceWrite](nil), b.source.preceding...), op.sourceWritten)
	}
}
