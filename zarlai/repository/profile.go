package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/zarldev/zarlmono/zarlai/repository/gen"
)

// Profile is the repository-layer representation of a profiles row.
type Profile struct {
	Name              string
	Model             string
	PromptPrefix      string
	MaxIterations     int
	ToolNames         []string
	ProviderWhitelist []string
	Source            string // "builtin" | "user"
	UpdatedAt         time.Time
}

// ProfileRepo persists profiles to the profiles table.
type ProfileRepo struct {
	q *gen.Queries
}

// NewProfileRepo wraps an sqlc Queries instance.
func NewProfileRepo(q *gen.Queries) *ProfileRepo {
	return &ProfileRepo{q: q}
}

// Get returns the profile row for name. Missing row => sql.ErrNoRows wrapped.
func (r *ProfileRepo) Get(ctx context.Context, name string) (Profile, error) {
	row, err := r.q.GetProfile(ctx, name)
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, fmt.Errorf("profile %q: %w", name, sql.ErrNoRows)
	}
	if err != nil {
		return Profile{}, fmt.Errorf("get profile %q: %w", name, err)
	}
	return rowToProfile(row), nil
}

// List returns all profiles ordered by source then name.
func (r *ProfileRepo) List(ctx context.Context) ([]Profile, error) {
	rows, err := r.q.ListProfiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("list profiles: %w", err)
	}
	out := make([]Profile, len(rows))
	for i, row := range rows {
		out[i] = rowToProfile(row)
	}
	return out, nil
}

// Upsert writes a profile row. Existing rows are updated in-place.
func (r *ProfileRepo) Upsert(ctx context.Context, p Profile) error {
	toolJSON := marshalStringSlice(p.ToolNames)
	providerJSON := marshalStringSlice(p.ProviderWhitelist)
	err := r.q.UpsertProfile(ctx, gen.UpsertProfileParams{
		Name:              p.Name,
		Model:             p.Model,
		PromptPrefix:      p.PromptPrefix,
		MaxIterations:     int32(p.MaxIterations),
		ToolNames:         toolJSON,
		ProviderWhitelist: providerJSON,
		Source:            gen.ProfilesSource(p.Source),
	})
	if err != nil {
		return fmt.Errorf("upsert profile %q: %w", p.Name, err)
	}
	return nil
}

// Delete removes a profile row. No error if the row does not exist.
func (r *ProfileRepo) Delete(ctx context.Context, name string) error {
	if err := r.q.DeleteProfile(ctx, name); err != nil {
		return fmt.Errorf("delete profile %q: %w", name, err)
	}
	return nil
}

// Count returns the number of rows in the profiles table.
func (r *ProfileRepo) Count(ctx context.Context) (int64, error) {
	n, err := r.q.CountProfiles(ctx)
	if err != nil {
		return 0, fmt.Errorf("count profiles: %w", err)
	}
	return n, nil
}

func rowToProfile(row gen.Profile) Profile {
	return Profile{
		Name:              row.Name,
		Model:             row.Model,
		PromptPrefix:      row.PromptPrefix,
		MaxIterations:     int(row.MaxIterations),
		ToolNames:         unmarshalStringSlice(row.ToolNames),
		ProviderWhitelist: unmarshalStringSlice(row.ProviderWhitelist),
		Source:            string(row.Source),
		UpdatedAt:         row.UpdatedAt,
	}
}

// marshalStringSlice serialises a string slice to a JSON array.
// nil or empty slice produces "[]".
func marshalStringSlice(s []string) string {
	if len(s) == 0 {
		return "[]"
	}
	b, err := json.Marshal(s)
	if err != nil {
		slog.Warn("failed to marshal string slice, using []", "err", err)
		return "[]"
	}
	return string(b)
}

// unmarshalStringSlice parses a JSON array from the DB column.
// Empty or invalid JSON returns an empty (non-nil) slice.
func unmarshalStringSlice(s string) []string {
	if s == "" || s == "null" {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		slog.Warn("string slice column contained invalid JSON, using []", "value", s, "err", err)
		return []string{}
	}
	if out == nil {
		return []string{}
	}
	return out
}
