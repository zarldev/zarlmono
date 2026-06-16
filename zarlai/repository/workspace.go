package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/zarldev/zarlmono/zarlai/repository/gen"
)

// Workspace is the repository-layer representation of a workspaces row.
type Workspace struct {
	Name          string
	Root          string
	DefaultBranch string
	Description   string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ErrDefaultWorkspaceProtected is returned when Delete is called on the
// seeded "default" row. The migration guarantees this row exists, and the
// runner relies on it as a fallback for tasks that omit a workspace name.
var ErrDefaultWorkspaceProtected = errors.New("workspace \"default\" cannot be deleted")

// WorkspaceRepo persists workspace rows.
type WorkspaceRepo struct {
	q *gen.Queries
}

// NewWorkspaceRepo wraps an sqlc Queries instance.
func NewWorkspaceRepo(q *gen.Queries) *WorkspaceRepo {
	return &WorkspaceRepo{q: q}
}

// Get returns the workspace row for name. Missing row → sql.ErrNoRows wrapped.
func (r *WorkspaceRepo) Get(ctx context.Context, name string) (Workspace, error) {
	row, err := r.q.GetWorkspace(ctx, name)
	if errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, fmt.Errorf("workspace %q: %w", name, sql.ErrNoRows)
	}
	if err != nil {
		return Workspace{}, fmt.Errorf("get workspace %q: %w", name, err)
	}
	return rowToWorkspace(row), nil
}

// List returns all workspaces ordered by name.
func (r *WorkspaceRepo) List(ctx context.Context) ([]Workspace, error) {
	rows, err := r.q.ListWorkspaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	out := make([]Workspace, len(rows))
	for i, row := range rows {
		out[i] = rowToWorkspace(row)
	}
	return out, nil
}

// Upsert inserts or updates by primary key (name).
func (r *WorkspaceRepo) Upsert(ctx context.Context, w Workspace) error {
	err := r.q.UpsertWorkspace(ctx, gen.UpsertWorkspaceParams{
		Name:          w.Name,
		Root:          w.Root,
		DefaultBranch: w.DefaultBranch,
		Description:   w.Description,
	})
	if err != nil {
		return fmt.Errorf("upsert workspace %q: %w", w.Name, err)
	}
	return nil
}

// Delete removes the row. The seeded "default" is refused.
func (r *WorkspaceRepo) Delete(ctx context.Context, name string) error {
	if name == "default" {
		return ErrDefaultWorkspaceProtected
	}
	if err := r.q.DeleteWorkspace(ctx, name); err != nil {
		return fmt.Errorf("delete workspace %q: %w", name, err)
	}
	return nil
}

func rowToWorkspace(row gen.Workspace) Workspace {
	return Workspace{
		Name:          row.Name,
		Root:          row.Root,
		DefaultBranch: row.DefaultBranch,
		Description:   row.Description,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}
}
