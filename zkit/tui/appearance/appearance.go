// Package appearance persists scoped theme selections and provides a preview-only picker.
package appearance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"

	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

// DefaultTheme is the palette used when no scope has an override.
const DefaultTheme = "ansi-bbs"

const (
	scopeGlobal    = "global"
	scopeApp       = "app"
	scopeWorkspace = "workspace"
)

// Target identifies exactly one override scope. Construct it with Global, App or Workspace.
type Target struct{ scope, app, workspace string }

// Global selects the override shared by all applications.
func Global() Target { return Target{scope: scopeGlobal} }

// App selects an application's override.
func App(app string) Target { return Target{scope: scopeApp, app: app} }

// Workspace selects an application's workspace override.
func Workspace(app, workspace string) Target {
	return Target{scope: scopeWorkspace, app: app, workspace: workspace}
}

// Label returns the target's scope.
func (t Target) Label() string { return t.scope }

// TargetFor parses an external scope and rejects missing application/workspace identifiers.
func TargetFor(scope, app, workspace string) (Target, error) {
	var t Target
	switch scope {
	case scopeGlobal:
		t = Global()
	case scopeApp:
		t = App(app)
	case scopeWorkspace:
		t = Workspace(app, workspace)
	default:
		return Target{}, fmt.Errorf("appearance: invalid scope %q", scope)
	}
	if err := t.validate(); err != nil {
		return Target{}, err
	}
	return t, nil
}
func (t Target) validate() error {
	switch t.scope {
	case scopeGlobal:
		if t.app == "" && t.workspace == "" {
			return nil
		}
	case scopeApp:
		if t.app != "" && t.workspace == "" {
			return nil
		}
	case scopeWorkspace:
		if t.app != "" && t.workspace != "" {
			return nil
		}
	}
	return fmt.Errorf("appearance: invalid %q target", t.scope)
}

// Choice is the effective theme and the scope from which it was inherited.
type Choice struct {
	Name  string
	Scope string
	Theme theme.Theme
}

// Store owns a path; each operation opens and closes its own database handle.
type Store struct{ path string }

// New constructs a store at path without accessing the filesystem.
func New(path string) *Store { return &Store{path: path} }

// DefaultPath returns the shared appearance database path without creating it.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("appearance: config directory: %w", err)
	}
	return filepath.Join(dir, "zarl", "appearance.db"), nil
}
func fallback() Choice {
	t, ok := theme.ByName(DefaultTheme)
	if !ok {
		t = theme.DarkDefault()
	}
	return Choice{Name: t.Name, Scope: "default", Theme: t}
}
func (s *Store) exists() (bool, error) {
	_, err := os.Stat(s.path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("appearance: stat %s: %w", s.path, err)
}
func (s *Store) open(readonly bool) (*sql.DB, error) {
	abs, err := filepath.Abs(s.path)
	if err != nil {
		return nil, fmt.Errorf("appearance: path: %w", err)
	}
	mode := "rw"
	if readonly {
		mode = "ro"
	}
	u := url.URL{Scheme: "file", Path: abs}
	q := u.Query()
	q.Set("mode", mode)
	q.Add("_pragma", "busy_timeout(1500)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("appearance: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

const schema = `CREATE TABLE IF NOT EXISTS overrides (scope TEXT NOT NULL, app TEXT NOT NULL, workspace TEXT NOT NULL, name TEXT NOT NULL, PRIMARY KEY(scope,app,workspace))`

// Lookup reads only the exact target. The returned name may be unknown to the current catalogue.
func (s *Store) Lookup(ctx context.Context, t Target) (string, bool, error) {
	if err := t.validate(); err != nil {
		return "", false, err
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	exists, err := s.exists()
	if err != nil {
		return "", false, err
	}
	if !exists {
		return "", false, nil
	}
	db, err := s.open(true)
	if err != nil {
		return "", false, err
	}
	defer db.Close()
	ready, err := hasSchema(ctx, db)
	if err != nil || !ready {
		return "", false, err
	}
	var name string
	err = db.QueryRowContext(ctx, `SELECT name FROM overrides WHERE scope=? AND app=? AND workspace=?`, t.scope, t.app, t.workspace).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("appearance: lookup %s: %w", t.scope, err)
	}
	return name, true, nil
}

// Resolve returns the most specific valid override, or the usable default. Corrupt
// storage or an unknown persisted name returns the fallback alongside a diagnostic.
func (s *Store) Resolve(ctx context.Context, app, workspace string) (Choice, error) {
	base := fallback()
	if workspace != "" && app == "" {
		return base, errors.New("appearance: workspace requires app")
	}
	targets := []Target{Global()}
	if app != "" {
		targets = append(targets, App(app))
	}
	if workspace != "" {
		targets = append(targets, Workspace(app, workspace))
	}
	if err := ctx.Err(); err != nil {
		return base, err
	}
	exists, err := s.exists()
	if err != nil {
		return base, err
	}
	if !exists {
		return base, nil
	}
	db, err := s.open(true)
	if err != nil {
		return base, err
	}
	defer db.Close()
	ready, err := hasSchema(ctx, db)
	if err != nil || !ready {
		return base, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return base, fmt.Errorf("appearance: read snapshot: %w", err)
	}
	defer func() {
		// Best-effort rollback also runs after a successful read snapshot.
		_ = tx.Rollback()
	}()
	var diagnostic error
	for i := len(targets) - 1; i >= 0; i-- {
		t := targets[i]
		var name string
		err = tx.QueryRowContext(ctx, `SELECT name FROM overrides WHERE scope=? AND app=? AND workspace=?`, t.scope, t.app, t.workspace).Scan(&name)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return base, fmt.Errorf("appearance: resolve %s: %w", t.scope, err)
		}
		palette, ok := theme.ByName(name)
		if name == "" || !ok {
			diagnostic = errors.Join(diagnostic, fmt.Errorf("appearance: unknown persisted theme %q at %s", name, t.scope))
			continue
		}
		return Choice{Name: name, Scope: t.scope, Theme: palette}, diagnostic
	}
	return base, diagnostic
}

// Set atomically saves a catalogue theme at the exact target.
func (s *Store) Set(ctx context.Context, t Target, name string) error {
	if err := t.validate(); err != nil {
		return err
	}
	if _, ok := theme.ByName(name); name == "" || !ok {
		return fmt.Errorf("appearance: unknown theme %q", name)
	}
	return s.mutate(ctx, `INSERT INTO overrides(scope,app,workspace,name) VALUES(?,?,?,?) ON CONFLICT(scope,app,workspace) DO UPDATE SET name=excluded.name`, t, true, name)
}

// Reset removes the exact override, allowing inheritance; a missing database stays absent.
func (s *Store) Reset(ctx context.Context, t Target) error {
	if err := t.validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	exists, err := s.exists()
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	return s.mutate(ctx, `DELETE FROM overrides WHERE scope=? AND app=? AND workspace=?`, t, false, "")
}
func (s *Store) mutate(ctx context.Context, query string, t Target, create bool, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if create {
		if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
			return fmt.Errorf("appearance: create directory: %w", err)
		}
		f, err := os.OpenFile(s.path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return fmt.Errorf("appearance: create database: %w", err)
		}
		if err = f.Close(); err != nil {
			return fmt.Errorf("appearance: close database file: %w", err)
		}
	}
	db, err := s.open(false)
	if err != nil {
		return err
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("appearance: connection: %w", err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "PRAGMA busy_timeout=10000"); err != nil {
		return fmt.Errorf("appearance: busy timeout: %w", err)
	}
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("appearance: begin: %w", err)
	}
	defer func() {
		// Best-effort rollback also runs after a successful commit.
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
	}()
	if _, err = conn.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("appearance: schema: %w", err)
	}
	args := []any{t.scope, t.app, t.workspace}
	if create {
		args = append(args, name)
	}
	if _, err = conn.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("appearance: update %s: %w", t.scope, err)
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("appearance: commit: %w", err)
	}
	return nil
}
