package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/zarldev/zarlmono/zkit/db/gen"
)

// GetSetting resolves a (workspace, key) lookup with global
// fallback. The workspace-specific value wins; otherwise the global
// row (workspace="") is returned. Returns ("", false, nil) when
// neither row exists — absence is not an error.
func (s *Store) GetSetting(ctx context.Context, workspace, key string) (string, bool, error) {
	if v, ok, err := s.getSettingRow(ctx, workspace, key); err != nil || ok {
		return v, ok, err
	}
	if workspace == "" {
		return "", false, nil
	}
	return s.getSettingRow(ctx, "", key)
}

// GetSettingExact is GetSetting without the global fallback — useful
// when callers need to distinguish "this value came from the workspace
// row" from "this value was inherited from the global row". Returns
// ("", false, nil) when no row exists for the exact (workspace, key).
func (s *Store) GetSettingExact(ctx context.Context, workspace, key string) (string, bool, error) {
	return s.getSettingRow(ctx, workspace, key)
}

func (s *Store) getSettingRow(ctx context.Context, workspace, key string) (string, bool, error) {
	v, err := s.q.GetSetting(ctx, gen.GetSettingParams{Workspace: workspace, Key: key})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("get setting %q/%q: %w", workspace, key, err)
	}
	return v, true, nil
}

// SetSetting writes a (workspace, key) pair. Pass workspace="" for
// the global default that other workspaces inherit.
func (s *Store) SetSetting(ctx context.Context, workspace, key, value string) error {
	err := s.q.UpsertSetting(ctx, gen.UpsertSettingParams{
		Workspace: workspace,
		Key:       key,
		Value:     value,
		UpdatedAt: time.Now().Unix(),
	})
	if err != nil {
		return fmt.Errorf("upsert setting %q/%q: %w", workspace, key, err)
	}
	return nil
}

// DeleteSetting removes a (workspace, key) pair. No-op when absent.
// The fallback to a global row, if any, becomes the effective value.
func (s *Store) DeleteSetting(ctx context.Context, workspace, key string) error {
	err := s.q.DeleteSetting(ctx, gen.DeleteSettingParams{Workspace: workspace, Key: key})
	if err != nil {
		return fmt.Errorf("delete setting %q/%q: %w", workspace, key, err)
	}
	return nil
}

// EffectiveSettings returns the merged setting map for workspace:
// every key set globally, overlaid by the workspace-specific values.
// Useful at startup when the shell wants its whole preference set in
// one shot instead of per-key Get calls.
func (s *Store) EffectiveSettings(ctx context.Context, workspace string) (map[string]string, error) {
	globals, err := s.q.ListSettingsByWorkspace(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("list global settings: %w", err)
	}
	out := make(map[string]string, len(globals))
	for _, r := range globals {
		out[r.Key] = r.Value
	}
	if workspace != "" {
		local, err := s.q.ListSettingsByWorkspace(ctx, workspace)
		if err != nil {
			return nil, fmt.Errorf("list workspace settings: %w", err)
		}
		for _, r := range local {
			out[r.Key] = r.Value
		}
	}
	return out, nil
}
