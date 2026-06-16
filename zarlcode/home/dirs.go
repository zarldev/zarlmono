package home

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"

	"github.com/zarldev/zarlmono/zkit/db"
)

// SafeUserHomeDir returns the user's home directory as a CLEAN
// ABSOLUTE path, falling back to the passwd database when the
// environment-derived value is missing or relative.
//
// Why this exists: [os.UserHomeDir] returns whatever is in $HOME on
// Unix without validating it's absolute. A misconfigured launcher
// (env scrub, sourced rc that did `HOME=home/$USER`, a tmuxinator
// recipe, an `env -i` invocation in someone's editor config) can
// leave the process with `HOME=home/bruno` and the rest of the
// zarlcode startup happily creates `home/bruno/.zarlcode`,
// `home/bruno/.zarlcode/cache/logs`, etc. RELATIVE to the
// current working directory. Sessions land in different on-disk
// homes depending on where the binary was launched from — silently
// — and the user accumulates stray dot-folders under random
// project trees.
//
// The check + fallback collapses the failure mode to a single
// well-tested helper. Every zarlcode consumer that needs the
// user's home (state.db, logs) goes through here.
func SafeUserHomeDir() (string, error) {
	if home, err := os.UserHomeDir(); err == nil && filepath.IsAbs(home) {
		return filepath.Clean(home), nil
	}
	// $HOME is missing or relative — consult the passwd database
	// directly. user.Current() invokes getent on cgo builds and
	// reads /etc/passwd otherwise; either way the result is the
	// canonical OS-level home, not a corrupted env value.
	u, uerr := user.Current()
	if uerr != nil {
		return "", fmt.Errorf("safe home dir: $HOME unset/relative and passwd lookup failed: %w", uerr)
	}
	if u.HomeDir == "" {
		return "", errors.New("safe home dir: passwd database has empty home for current user")
	}
	if !filepath.IsAbs(u.HomeDir) {
		return "", fmt.Errorf("safe home dir: passwd database home %q is not absolute", u.HomeDir)
	}
	return filepath.Clean(u.HomeDir), nil
}

// HomeDir returns the canonical state home — same path as
// [db.DefaultDir]. Re-exposed here so non-db callers don't have to
// import the db package just to ask "where does my stuff live".
func HomeDir() (string, error) { return db.DefaultDir() }

// CacheDir returns ~/.zarlcode/cache. Logs, transient
// artefacts, and any future spool files land here.
func CacheDir() (string, error) {
	home, err := HomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "cache"), nil
}

// ConfigDir returns ~/.zarlcode/config — the home for
// per-user themes, agents, skills, prompts, and the global .env.
func ConfigDir() (string, error) {
	home, err := HomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "config"), nil
}

// WorkspaceDir returns <wsRoot>/.zarlcode. Empty wsRoot
// yields "" — the caller decides whether that's an error.
func WorkspaceDir(wsRoot string) string {
	if wsRoot == "" {
		return ""
	}
	return filepath.Join(wsRoot, "."+db.AppName)
}
