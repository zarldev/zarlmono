package migrations

import (
	"context"
	"database/sql"
	"fmt"
)

// Migration 30 shipped with two table shapes. Repair only missing columns;
// existing classifications and history IDs must remain untouched. Goose owns
// the transaction, so schema changes and its version receipt commit together.
func addToolHistoryClassification(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "PRAGMA table_info(tool_output_history)")
	if err != nil {
		return fmt.Errorf("inspect tool history columns: %w", err)
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return fmt.Errorf("scan tool history column: %w", err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate tool history columns: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close tool history columns: %w", err)
	}
	// A missing table is corruption, not the known version-30 variant. Let
	// ALTER fail rather than mark an unrepaired database current.
	for _, column := range []struct {
		name string
		sql  string
	}{
		{"success", "ALTER TABLE tool_output_history ADD COLUMN success INTEGER NOT NULL DEFAULT 0 CHECK (success IN (0, 1))"},
		{"error", "ALTER TABLE tool_output_history ADD COLUMN error TEXT NOT NULL DEFAULT ''"},
		{"kind", "ALTER TABLE tool_output_history ADD COLUMN kind TEXT NOT NULL DEFAULT 'unknown'"},
	} {
		if !columns[column.name] {
			if _, err := tx.ExecContext(ctx, column.sql); err != nil {
				return fmt.Errorf("add tool history %s: %w", column.name, err)
			}
		}
	}
	return nil
}
