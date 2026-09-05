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
	ID                 string
	Workspace          string
	Label              string
	LabelManual        bool
	AgentName          string
	Provider           string
	Model              string
	ContextJSON        []byte
	PendingJSON        []byte
	LastUsageJSON      []byte
	DiffBodiesJSON     []byte
	PlanJSON           []byte
	MessageCount       int
	CreatedAt          time.Time
	Pinned             bool
	PinnedAt           time.Time
	ChangedFileCount   int
	PlanCompletedCount int
	PlanTotalCount     int
	HasDraft           bool
	HasTranscript      bool
	UpdatedAt          time.Time
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

// ListSessionSummaries returns resumable-session metadata without loading
// large context, diff, or plan JSON blobs.
func (s *Store) ListSessionSummaries(ctx context.Context, workspace string) ([]SessionRecord, error) {
	rows, err := s.q.ListSessionSummariesByWorkspace(ctx, workspace)
	if err != nil {
		return nil, fmt.Errorf("list session summaries: %w", err)
	}
	out := make([]SessionRecord, len(rows))
	for i, r := range rows {
		out[i] = sessionSummaryRowToRecord(r)
	}
	return out, nil
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
		out[i] = toSessionRecord(gen.GetSessionRow(r))
	}
	return out, nil
}

const activeSessionSettingKey = "active_session"

// SaveActiveSession atomically upserts a session and marks it active for its
// workspace. Neither write is committed unless both succeed.
func (s *Store) SaveActiveSession(ctx context.Context, r SessionRecord) error {
	if err := s.WithTx(ctx, func(tx *Store) error {
		if err := tx.SaveSession(ctx, r); err != nil {
			return err
		}
		if err := tx.SetSetting(ctx, r.Workspace, activeSessionSettingKey, r.ID); err != nil {
			return fmt.Errorf("mark session %q active: %w", r.ID, err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("save active session %q: %w", r.ID, err)
	}
	return nil
}

// SaveSession upserts the record. CreatedAt is preserved (treated as
// metadata the caller owns); UpdatedAt is replaced with time.Now().
func (s *Store) SaveSession(ctx context.Context, r SessionRecord) error {
	if err := s.ensureSessionWorkspace(ctx, r.ID, r.Workspace); err != nil {
		return err
	}
	now := time.Now()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	r.UpdatedAt = now
	err := s.q.UpsertSession(ctx, gen.UpsertSessionParams{
		ID:                 r.ID,
		Workspace:          r.Workspace,
		LabelManual:        boolToInt64(r.LabelManual),
		Label:              r.Label,
		AgentName:          r.AgentName,
		Provider:           r.Provider,
		Model:              r.Model,
		ContextJson:        string(orEmpty(r.ContextJSON, "[]")),
		PendingJson:        string(orEmpty(r.PendingJSON, "[]")),
		LastUsageJson:      string(orEmpty(r.LastUsageJSON, "null")),
		DiffBodiesJson:     string(orEmpty(r.DiffBodiesJSON, "{}")),
		PlanJson:           string(orEmpty(r.PlanJSON, "null")),
		MessageCount:       int64(r.MessageCount),
		CreatedAt:          r.CreatedAt.Unix(),
		UpdatedAt:          r.UpdatedAt.Unix(),
		ChangedFileCount:   int64(r.ChangedFileCount),
		PlanCompletedCount: int64(r.PlanCompletedCount),
		PlanTotalCount:     int64(r.PlanTotalCount),
	})
	if err != nil {
		return fmt.Errorf("upsert session %q: %w", r.ID, err)
	}
	return nil
}
func (s *Store) ensureSessionWorkspace(ctx context.Context, id, workspace string) error {
	existing, err := s.GetSession(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read session %q workspace: %w", id, err)
	}
	if existing.Workspace != workspace {
		return fmt.Errorf("session %q belongs to workspace %q, not %q", id, existing.Workspace, workspace)
	}
	return nil
}

// SaveSessionDraft upserts only pending draft content. Existing canonical
// session metadata and activity timestamps are preserved on conflict.
func (s *Store) SaveSessionDraft(ctx context.Context, r SessionRecord) error {
	if err := s.ensureSessionWorkspace(ctx, r.ID, r.Workspace); err != nil {
		return err
	}
	now := time.Now()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	if err := s.q.SaveSessionDraft(ctx, gen.SaveSessionDraftParams{
		ID: r.ID, Workspace: r.Workspace, Label: r.Label, LabelManual: boolToInt64(r.LabelManual),
		AgentName: r.AgentName, Provider: r.Provider, Model: r.Model,
		PendingJson: string(orEmpty(r.PendingJSON, "[]")), CreatedAt: r.CreatedAt.Unix(), UpdatedAt: now.Unix(),
	}); err != nil {
		return fmt.Errorf("save session %q draft: %w", r.ID, err)
	}
	return nil
}

// ClearSessionDraft removes pending draft content without changing canonical
// context or conversation activity timestamps. A draft-only row is deleted
// once both its context and pending content are empty.
func (s *Store) ClearSessionDraft(ctx context.Context, id string) error {
	if err := s.WithTx(ctx, func(tx *Store) error {
		if err := tx.q.ClearSessionDraft(ctx, id); err != nil {
			return fmt.Errorf("clear pending content: %w", err)
		}
		if err := tx.q.DeleteEmptySession(ctx, id); err != nil {
			return fmt.Errorf("delete empty session: %w", err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("clear session %q draft: %w", id, err)
	}
	return nil
}

// DeleteEmptySession removes a session with no model context and no pending
// draft. No-op when the row is absent or has content.
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

// RenameSession updates only a saved session's user-visible label. It preserves
// canonical context, timestamps, message counts, and every other persisted field.
func (s *Store) RenameSession(ctx context.Context, id, label string) error {
	return s.q.RenameSession(ctx, gen.RenameSessionParams{Label: label, ID: id})
}

// SetSessionPinned sets a saved session's pin state without changing its
// conversation activity timestamp.
func (s *Store) SetSessionPinned(ctx context.Context, id string, pinned bool, pinnedAt time.Time) error {
	var pinnedValue int64
	var pinnedTime sql.NullInt64
	if pinned {
		pinnedValue = 1
		pinnedTime = sql.NullInt64{Int64: pinnedAt.Unix(), Valid: true}
	}
	if err := s.q.SetSessionPinned(ctx, gen.SetSessionPinnedParams{Pinned: pinnedValue, PinnedAt: pinnedTime, ID: id}); err != nil {
		return fmt.Errorf("set session %q pinned: %w", id, err)
	}
	return nil
}
