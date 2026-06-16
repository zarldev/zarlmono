package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/zarldev/zarlmono/zarlai/repository/gen"
)

// ToolDescriptionOverride is a human-edited replacement for a tool's
// code-default description string. Nothing else about the tool is
// overridable — schema and behaviour stay in code.
type ToolDescriptionOverride struct {
	Name        string
	Description string
	UpdatedAt   time.Time
}

type ToolDescriptionOverrideRepo struct {
	q *gen.Queries
}

func NewToolDescriptionOverrideRepo(q *gen.Queries) *ToolDescriptionOverrideRepo {
	return &ToolDescriptionOverrideRepo{q: q}
}

// Get returns the override for the named tool, or sql.ErrNoRows when
// none exists. Callers usually want List for bulk load; this is here for
// targeted refreshes.
func (r *ToolDescriptionOverrideRepo) Get(ctx context.Context, name string) (ToolDescriptionOverride, error) {
	row, err := r.q.GetToolDescriptionOverride(ctx, name)
	if err != nil {
		return ToolDescriptionOverride{}, fmt.Errorf("get tool description override %q: %w", name, err)
	}
	return ToolDescriptionOverride{
		Name:        row.Name,
		Description: row.Description,
		UpdatedAt:   row.UpdatedAt,
	}, nil
}

// List returns every override row. Used on startup to warm the in-memory
// store and on admin list views.
func (r *ToolDescriptionOverrideRepo) List(ctx context.Context) ([]ToolDescriptionOverride, error) {
	rows, err := r.q.ListToolDescriptionOverrides(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tool description overrides: %w", err)
	}
	out := make([]ToolDescriptionOverride, len(rows))
	for i, row := range rows {
		out[i] = ToolDescriptionOverride{
			Name:        row.Name,
			Description: row.Description,
			UpdatedAt:   row.UpdatedAt,
		}
	}
	return out, nil
}

// Upsert writes or replaces the override for a tool name.
func (r *ToolDescriptionOverrideRepo) Upsert(ctx context.Context, name, description string) error {
	if name == "" {
		return errors.New("upsert tool description override: name is empty")
	}
	if err := r.q.UpsertToolDescriptionOverride(ctx, gen.UpsertToolDescriptionOverrideParams{
		Name:        name,
		Description: description,
	}); err != nil {
		return fmt.Errorf("upsert tool description override %q: %w", name, err)
	}
	return nil
}

// Delete removes the override so the code-default description takes
// effect again. Safe when no row exists.
func (r *ToolDescriptionOverrideRepo) Delete(ctx context.Context, name string) error {
	if err := r.q.DeleteToolDescriptionOverride(ctx, name); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("delete tool description override %q: %w", name, err)
	}
	return nil
}
