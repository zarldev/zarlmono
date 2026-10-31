package appearance

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
)

// hasSchema treats an empty, interrupted first creation as an absent selection,
// while malformed SQLite and incompatible schemas still produce diagnostics.
func hasSchema(ctx context.Context, db *sql.DB) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='overrides'`).Scan(&count); err != nil {
		return false, fmt.Errorf("appearance: read schema: %w", err)
	}
	return count != 0, nil
}

// CanonicalWorkspace resolves a caller-supplied workspace to a stable absolute
// path. Existing symlinks resolve to their target; future workspace paths remain
// clean absolute paths. It does not create directories or inspect account data.
func CanonicalWorkspace(path string) (string, error) {
	if path == "" {
		var err error
		path, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("appearance: workspace directory: %w", err)
		}
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("appearance: workspace path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		return resolved, nil
	}
	if os.IsNotExist(err) {
		return filepath.Clean(absolute), nil
	}
	return "", fmt.Errorf("appearance: resolve workspace: %w", err)
}
