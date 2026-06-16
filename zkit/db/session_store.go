package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/zarldev/zarlmono/zkit/db/gen"
)

// SessionRecord is the store's transport-shape view of a saved
// session. JSON-blob fields stay as []byte; the caller marshals /
// unmarshals against whatever runtime types it wants (the shell
// uses llm.Message / fileaudit.Entry).
type SessionRecord struct {
	ID             string
	Workspace      string
	Label          string
	AgentName      string
	Provider       string
	Model          string
	HistoryJSON    []byte
	PendingJSON    []byte
	LastUsageJSON  []byte
	DiffBodiesJSON []byte
	PlanJSON       []byte
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// GetSession fetches one session by id. Returns [ErrNotFound] when
// the row is absent so callers can branch without importing
// database/sql.
func (s *Store) GetSession(ctx context.Context, id string) (SessionRecord, error) {
	row, err := s.q.GetSession(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SessionRecord{}, ErrNotFound
		}
		return SessionRecord{}, fmt.Errorf("get session %q: %w", id, err)
	}
	return toSessionRecord(row), nil
}

// ListSessions returns every session for the workspace, most recent
// first. Empty slice (not nil) when no sessions are stored.
func (s *Store) ListSessions(ctx context.Context, workspace string) ([]SessionRecord, error) {
	rows, err := s.q.ListSessionsByWorkspace(ctx, workspace)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	out := make([]SessionRecord, len(rows))
	for i, r := range rows {
		out[i] = toSessionRecord(r)
	}
	return out, nil
}

// SaveSession upserts the record. CreatedAt is preserved (treated as
// metadata the caller owns); UpdatedAt is replaced with time.Now().
func (s *Store) SaveSession(ctx context.Context, r SessionRecord) error {
	now := time.Now()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	r.UpdatedAt = now
	err := s.q.UpsertSession(ctx, gen.UpsertSessionParams{
		ID:             r.ID,
		Workspace:      r.Workspace,
		Label:          r.Label,
		AgentName:      r.AgentName,
		Provider:       r.Provider,
		Model:          r.Model,
		HistoryJson:    string(orEmpty(r.HistoryJSON, "[]")),
		PendingJson:    string(orEmpty(r.PendingJSON, "[]")),
		LastUsageJson:  string(orEmpty(r.LastUsageJSON, "null")),
		DiffBodiesJson: string(orEmpty(r.DiffBodiesJSON, "{}")),
		PlanJson:       string(orEmpty(r.PlanJSON, "null")),
		CreatedAt:      r.CreatedAt.Unix(),
		UpdatedAt:      r.UpdatedAt.Unix(),
	})
	if err != nil {
		return fmt.Errorf("upsert session %q: %w", r.ID, err)
	}
	return nil
}

// DeleteEmptySession removes a session with no history and no
// pending attachments. No-op when the row is absent or has content.
func (s *Store) DeleteEmptySession(ctx context.Context, id string) error {
	if err := s.q.DeleteEmptySession(ctx, id); err != nil {
		return fmt.Errorf("delete empty session %q: %w", id, err)
	}
	return nil
}

// DeleteSession unconditionally removes a session. Used by an
// eventual /session delete UX.
func (s *Store) DeleteSession(ctx context.Context, id string) error {
	if err := s.q.DeleteSession(ctx, id); err != nil {
		return fmt.Errorf("delete session %q: %w", id, err)
	}
	return nil
}
