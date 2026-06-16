// Package db is the zarlcode persistence layer. State that is
// machine-managed and queryable lives here (sessions, settings, api
// keys, future cost ledger). Human-editable artefacts (prompt.md,
// skills/, tools/) stay on the filesystem under ~/.zarlcode.
//
// The store wraps the sqlc-generated [gen.Queries] in domain methods
// that map between gen's int64 timestamps + tagless rows and the
// shell's time.Time / typed records. Errors from sql.ErrNoRows are
// translated into typed sentinels ([ErrNotFound]) so callers can
// branch on absence without importing database/sql.
package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // sqlite driver — pure Go, no CGO.

	"github.com/zarldev/zarlmono/zkit/db/gen"
	"github.com/zarldev/zarlmono/zkit/filesystem"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ErrNotFound is returned when a Get does not match a row. Callers
// branch on it via errors.Is.
var ErrNotFound = errors.New("zarlcode/db: not found")

// AppName is the canonical brand. Every dot-folder under $HOME,
// every cache / config dir, the package path, and every
// user-facing string normalize to this single name.
const AppName = "zarlcode"

// DefaultDir is the on-disk home for zarlcode state. The sqlite
// file lives at filepath.Join(DefaultDir(), "state.db"); per-user
// editable artefacts (prompt.md, skills/, tools/) share the
// directory.
//
// The home is resolved with a guard against $HOME being unset or
// relative — see [resolveUserHome]. Without the guard a stray
// `HOME=home/bruno` env (from a misconfigured launcher / sourced
// rc / `env -i` invocation) leaves the rest of the bootstrap
// happily creating `home/bruno/.zarlcode/` RELATIVE to the
// current working directory and silently scattering session state
// across project trees.
func DefaultDir() (string, error) {
	home, err := resolveUserHome()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, "."+AppName), nil
}

// resolveUserHome returns a CLEAN ABSOLUTE path for the user's home
// directory. Falls back from $HOME to the passwd database when
// $HOME is missing or relative. Same logic the zarlcode main
// package uses for cache / config dirs — duplicated here so the
// db package can stay leaf-importable from main without a cycle.
func resolveUserHome() (string, error) {
	if home, err := os.UserHomeDir(); err == nil && filepath.IsAbs(home) {
		return filepath.Clean(home), nil
	}
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("$HOME unset/relative and passwd lookup failed: %w", err)
	}
	if u.HomeDir == "" || !filepath.IsAbs(u.HomeDir) {
		return "", fmt.Errorf("passwd home %q is empty or not absolute", u.HomeDir)
	}
	return filepath.Clean(u.HomeDir), nil
}

// DefaultPath returns the canonical state.db location. Honoured by
// [Open] when callers pass an empty string.
func DefaultPath() (string, error) {
	d, err := DefaultDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "state.db"), nil
}

// Store owns the sqlite connection and the sqlc Queries facade.
// Goroutine-safe by virtue of [database/sql.DB].
type Store struct {
	db *sql.DB
	q  *gen.Queries
}

// Open returns a Store backed by the sqlite file at path. When path
// is empty it resolves to [DefaultPath]. Parent directories are
// created/hardened to filesystem.ModePrivateDir and the sqlite database plus WAL/SHM
// sidecars are hardened to filesystem.ModePrivateFile. Embedded migrations are applied
// via goose before Open returns, so callers see a schema-current DB.
//
// Subsequent calls to Open against the same path are safe; sqlite
// serialises writes and goose is idempotent.
func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	if err := hardenSQLitePath(path); err != nil {
		return nil, err
	}
	// Pragmas: WAL for concurrent reads + foreign_keys for safety,
	// busy_timeout to avoid SQLITE_BUSY in a short window between
	// the agent loop and any human "/session pick" query.
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)"
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	if err := d.PingContext(ctx); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if err := migrate(ctx, d); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if err := chmodSQLiteFiles(path); err != nil {
		_ = d.Close()
		return nil, err
	}
	return &Store{db: d, q: gen.New(d)}, nil
}

func hardenSQLitePath(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, filesystem.ModePrivateDir); err != nil {
		return fmt.Errorf("mkdir db dir: %w", err)
	}
	if err := os.Chmod(dir, filesystem.ModePrivateDir); err != nil {
		return fmt.Errorf("chmod db dir %q: %w", dir, err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, filesystem.ModePrivateFile)
	if err != nil {
		return fmt.Errorf("create sqlite %q: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close sqlite %q: %w", path, err)
	}
	return chmodSQLiteFiles(path)
}

func chmodSQLiteFiles(path string) error {
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(p, filesystem.ModePrivateFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("chmod sqlite file %q: %w", p, err)
		}
	}
	return nil
}

// Close releases the underlying connection. Safe to call on a nil
// receiver so deferred cleanup paths stay tidy.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// DB returns the underlying *sql.DB. Exposed so tests and the rare
// caller that needs raw access can reach in; normal code should go
// through the typed methods.
func (s *Store) DB() *sql.DB { return s.db }

func migrate(ctx context.Context, d *sql.DB) error {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("sub migrations: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, d, sub)
	if err != nil {
		return fmt.Errorf("goose provider: %w", err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	// Refuse a DB whose schema is NEWER than this binary understands (a newer
	// zarlcode upgraded it, then an older one opened it). Up() no-ops in that
	// case — it only applies migrations it knows — so without this guard the
	// old binary would happily write rows against a schema it can't see,
	// risking silent truncation or a NOT-NULL insert failure on a future
	// column. Up ran first so the version table exists for GetDBVersion.
	var maxKnown int64
	for _, src := range p.ListSources() {
		if src.Version > maxKnown {
			maxKnown = src.Version
		}
	}
	dbVer, err := p.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("goose db version: %w", err)
	}
	if dbVer > maxKnown {
		return fmt.Errorf("state.db schema version %d is newer than this binary understands (max %d) — upgrade zarlcode", dbVer, maxKnown)
	}
	return nil
}

// WithTx runs fn inside a single database transaction, passing a Store bound to
// that transaction. It commits when fn returns nil and rolls back otherwise —
// so a multi-statement sequence (e.g. prefs promote: write-global then
// delete-workspace) can't be left half-applied by a crash between statements.
func (s *Store) WithTx(ctx context.Context, fn func(*Store) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(&Store{db: s.db, q: s.q.WithTx(tx)}); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}
