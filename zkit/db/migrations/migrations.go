// Package migrations owns the complete SQLite migration catalogue, including
// conditional repairs for schema variants shipped under the same version.
package migrations

import (
	"database/sql"
	"embed"

	"github.com/pressly/goose/v3"
)

//go:embed *.sql
var files embed.FS

// NewProvider binds the embedded SQL and Go migrations to the borrowed database.
// The caller owns the database and chooses when to migrate it.
func NewProvider(database *sql.DB) (*goose.Provider, error) {
	// Down is deliberately a no-op: canonical version 30 already contains
	// these columns. Dropping them would lose classifications and recreate
	// the broken schema. Down to 29 still uses migration 30's table removal.
	return goose.NewProvider(goose.DialectSQLite3, database, files,
		goose.WithDisableGlobalRegistry(true),
		goose.WithGoMigrations(goose.NewGoMigration(31,
			&goose.GoFunc{RunTx: addToolHistoryClassification}, nil)))
}
