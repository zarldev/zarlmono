package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/zarldev/zarlmono/zarlai/repository/gen"
)

// TaskProfileOverride carries optional per-profile tuning knobs.
// A nil pointer means "not overridden — use the code default".
type TaskProfileOverride struct {
	Model         *string
	PromptPrefix  *string
	MaxIterations *int32
	// ToolNames, when non-nil and non-empty, restricts the profile to these tools.
	// nil means "no override — use the profile's baked-in whitelist".
	ToolNames []string
}

// TaskProfileOverrideRepo persists per-profile overrides.
type TaskProfileOverrideRepo struct {
	q *gen.Queries
}

// NewTaskProfileOverrideRepo wraps an sqlc Queries instance.
func NewTaskProfileOverrideRepo(q *gen.Queries) *TaskProfileOverrideRepo {
	return &TaskProfileOverrideRepo{q: q}
}

// Get returns the override for a profile. Missing row => zero TaskProfileOverride, nil error.
func (r *TaskProfileOverrideRepo) Get(ctx context.Context, profile string) (TaskProfileOverride, error) {
	row, err := r.q.GetTaskProfileOverride(ctx, profile)
	if errors.Is(err, sql.ErrNoRows) {
		return TaskProfileOverride{}, nil
	}
	if err != nil {
		return TaskProfileOverride{}, fmt.Errorf("get profile override %q: %w", profile, err)
	}
	return getRowToOverride(row), nil
}

// Upsert writes the override. Nil fields become NULL in the row.
func (r *TaskProfileOverrideRepo) Upsert(ctx context.Context, profile string, o TaskProfileOverride) error {
	err := r.q.UpsertTaskProfileOverride(ctx, gen.UpsertTaskProfileOverrideParams{
		ProfileName:   profile,
		Model:         stringPtrToNull(o.Model),
		PromptPrefix:  stringPtrToNull(o.PromptPrefix),
		MaxIterations: int32PtrToNull(o.MaxIterations),
		ToolNames:     toolNamesToNull(o.ToolNames),
	})
	if err != nil {
		return fmt.Errorf("upsert profile override %q: %w", profile, err)
	}
	return nil
}

// Delete removes the override. No error if the row does not exist.
func (r *TaskProfileOverrideRepo) Delete(ctx context.Context, profile string) error {
	if err := r.q.DeleteTaskProfileOverride(ctx, profile); err != nil {
		return fmt.Errorf("delete profile override %q: %w", profile, err)
	}
	return nil
}

// List returns every override row, keyed by profile name.
func (r *TaskProfileOverrideRepo) List(ctx context.Context) (map[string]TaskProfileOverride, error) {
	rows, err := r.q.ListTaskProfileOverrides(ctx)
	if err != nil {
		return nil, fmt.Errorf("list profile overrides: %w", err)
	}
	out := make(map[string]TaskProfileOverride, len(rows))
	for _, row := range rows {
		out[row.ProfileName] = listRowToOverride(row)
	}
	return out, nil
}

func getRowToOverride(row gen.GetTaskProfileOverrideRow) TaskProfileOverride {
	return TaskProfileOverride{
		Model:         nullToStringPtr(row.Model),
		PromptPrefix:  nullToStringPtr(row.PromptPrefix),
		MaxIterations: nullToInt32Ptr(row.MaxIterations),
		ToolNames:     nullToToolNames(row.ToolNames),
	}
}

func listRowToOverride(row gen.ListTaskProfileOverridesRow) TaskProfileOverride {
	return TaskProfileOverride{
		Model:         nullToStringPtr(row.Model),
		PromptPrefix:  nullToStringPtr(row.PromptPrefix),
		MaxIterations: nullToInt32Ptr(row.MaxIterations),
		ToolNames:     nullToToolNames(row.ToolNames),
	}
}

// nullToToolNames unmarshals a JSON array stored in a NullString column.
// An invalid/empty column returns nil (no override). Parse errors are logged and treated as nil.
func nullToToolNames(n sql.NullString) []string {
	if !n.Valid || n.String == "" {
		return nil
	}
	var names []string
	if err := json.Unmarshal([]byte(n.String), &names); err != nil {
		slog.Warn("tool_names column contained invalid JSON, ignoring", "value", n.String, "err", err)
		return nil
	}
	return names
}

// toolNamesToNull marshals a tool name slice to JSON for storage.
// nil or empty slice produces NULL (no override).
func toolNamesToNull(names []string) sql.NullString {
	if len(names) == 0 {
		return sql.NullString{}
	}
	b, err := json.Marshal(names)
	if err != nil {
		slog.Warn("failed to marshal tool_names, storing NULL", "err", err)
		return sql.NullString{}
	}
	return sql.NullString{String: string(b), Valid: true}
}

func stringPtrToNull(p *string) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *p, Valid: true}
}

func int32PtrToNull(p *int32) sql.NullInt32 {
	if p == nil {
		return sql.NullInt32{}
	}
	return sql.NullInt32{Int32: *p, Valid: true}
}

func nullToStringPtr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	s := n.String
	return &s
}

func nullToInt32Ptr(n sql.NullInt32) *int32 {
	if !n.Valid {
		return nil
	}
	v := n.Int32
	return &v
}
