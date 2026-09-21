// Package db is the zarlcode persistence layer. State that is
// machine-managed and queryable lives here (sessions, settings, api
// keys, future cost ledger). Human-editable artefacts (preferences.md,
// prompt.override.md, skills/, tools/) stay on the filesystem under ~/.zarlcode.
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
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/user"
	"path/filepath"

	_ "modernc.org/sqlite" // sqlite driver — pure Go, no CGO.

	"github.com/zarldev/zarlmono/zkit/db/gen"
	"github.com/zarldev/zarlmono/zkit/db/migrations"
	"github.com/zarldev/zarlmono/zkit/filesystem"
)

// ErrNotFound is returned when a Get does not match a row. Callers
// branch on it via errors.Is.
var ErrNotFound = errors.New("zarlcode/db: not found")

// AppName is the canonical brand. Every dot-folder under $HOME,
// every cache / config dir, the package path, and every
// user-facing string normalize to this single name.
const AppName = "zarlcode"

// DefaultDir is the on-disk home for zarlcode state. The sqlite
// file lives at filepath.Join(DefaultDir(), "state.db"); per-user
// editable artefacts (preferences.md, prompt.override.md, skills/, tools/)
// share the directory. Legacy prompt.md files may still exist there as
// migration-only full prompt overrides.
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

// Store owns a single-connection writer pool and a bounded read-only pool.
// Goroutine-safe by virtue of [database/sql.DB].
type Store struct {
	db     *sql.DB
	reader *sql.DB
	q      *gen.Queries // writer, or the current transaction
	read   *gen.Queries // reader, or the same current transaction
	inTx   bool
}

const readerConnections = 4

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
	// Resolve and URI-escape the filesystem path so both pools open the same
	// database, including filenames containing SQLite URI metacharacters.
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve sqlite path: %w", err)
	}
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	// WAL allows the reader pool to progress while the writer is occupied.
	// The busy timeout also applies to contention with other processes.
	dsn := uri.String() + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(30000)"
	d, err := sql.Open("sqlite", dsn)

	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	// SQLite permits one writer. Queue this Store's writes in database/sql
	// instead of contending for SQLite's write lock between local connections.
	d.SetMaxOpenConns(1)
	d.SetMaxIdleConns(1)

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
	// Open readers only after creation and migration. mode=ro enforces read-only
	// access on every connection, including connections opened later by the pool.
	readDB, err := sql.Open("sqlite", uri.String()+"?mode=ro&_pragma=foreign_keys(ON)&_pragma=busy_timeout(30000)")
	if err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("open sqlite readers: %w", err)
	}
	readDB.SetMaxOpenConns(readerConnections)
	readDB.SetMaxIdleConns(readerConnections)
	if err := readDB.PingContext(ctx); err != nil {
		_ = readDB.Close()
		_ = d.Close()
		return nil, fmt.Errorf("ping sqlite readers: %w", err)
	}
	return &Store{db: d, reader: readDB, q: gen.New(d), read: gen.New(readDB)}, nil
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

// Close releases both owned pools. Safe to call on a nil receiver so deferred
// cleanup paths stay tidy.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return errors.Join(s.reader.Close(), s.db.Close())
}

// DB returns the single-connection writer pool for raw access. Ordinary reads
// should use typed methods or ReadDB so they do not queue behind writers.
func (s *Store) DB() *sql.DB { return s.db }

// ReadDB returns the bounded read-only pool for raw inspection. Callers must
// close rows and transactions promptly so readers do not hold back WAL cleanup.
// Store owns this pool; callers must not close it or change its connection limits.
func (s *Store) ReadDB() *sql.DB { return s.reader }

func migrate(ctx context.Context, d *sql.DB) error {
	p, err := migrations.NewProvider(d)
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
	return s.withPoolTx(ctx, s.db, fn)
}

// Snapshot reads borrow an existing transaction when called within a write;
// otherwise they use a reader connection without occupying the writer pool.
func (s *Store) withReadTx(ctx context.Context, fn func(*Store) error) error {
	if s.inTx {
		return fn(s)
	}
	return s.withPoolTx(ctx, s.reader, fn)
}

func (s *Store) withPoolTx(ctx context.Context, pool *sql.DB, fn func(*Store) error) error {
	tx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := gen.New(tx)
	if err := fn(&Store{db: s.db, reader: s.reader, q: queries, read: queries, inTx: true}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}
