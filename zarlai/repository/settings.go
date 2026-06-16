package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/zarldev/zarlmono/zarlai/repository/gen"
)

// SettingsRepo provides access to the key-value settings table.
type SettingsRepo struct {
	q *gen.Queries
}

// NewSettingsRepo creates a SettingsRepo.
func NewSettingsRepo(q *gen.Queries) *SettingsRepo {
	return &SettingsRepo{q: q}
}

// Get returns the value for a key. Returns empty string and no error when the key does not exist.
func (r *SettingsRepo) Get(ctx context.Context, key string) (string, error) {
	row, err := r.q.GetSetting(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get setting %q: %w", key, err)
	}
	return row.Value, nil
}

// Set upserts a key-value pair.
func (r *SettingsRepo) Set(ctx context.Context, key, value string) error {
	err := r.q.UpsertSetting(ctx, gen.UpsertSettingParams{Key: key, Value: value})
	if err != nil {
		return fmt.Errorf("set setting %q: %w", key, err)
	}
	return nil
}
