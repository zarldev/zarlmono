package home

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/filesystem"
)

// Result summarises what a [Materialise] pass did. Printed by the
// init subcommand; consulted by the implicit first-run path to decide
// whether to surface a "welcome" startup notice.
type Result struct {
	Dir     string   // ~/.zarlcode
	Created []string // newly-materialised relative paths
	Existed []string // already-present relative paths
}

// String renders the result for the init subcommand's stdout.
func (r Result) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "zarlcode home at %s\n", r.Dir)
	if len(r.Created) > 0 {
		fmt.Fprintf(&b, "  created: %s\n", strings.Join(r.Created, ", "))
	}
	if len(r.Existed) > 0 {
		fmt.Fprintf(&b, "  existed: %s\n", strings.Join(r.Existed, ", "))
	}
	return b.String()
}

// Materialise ensures ~/.zarlcode exists with the canonical
// directory layout (skills/, tools/, hooks/) plus state.db (created lazily by
// the first store Open). Idempotent — existing files are left exactly as they
// are, including legacy prompt files.
//
// Returns the [Result] describing what was touched. Any
// filesystem error short-circuits with an error; partial state on
// disk is allowed (the next run finishes the work).
func Materialise() (Result, error) {
	dir, err := db.DefaultDir()
	if err != nil {
		return Result{}, fmt.Errorf("home dir: %w", err)
	}
	res := Result{Dir: dir}

	if err := os.MkdirAll(dir, filesystem.ModePublicDir); err != nil {
		return res, fmt.Errorf("mkdir %q: %w", dir, err)
	}

	// Make user-editable directories discoverable. Empty is fine —
	// the shell scans them at every launch; a freshly-installed user
	// finds out where to drop content by listing the home directory.
	for _, sub := range []string{"skills", "tools", "hooks"} {
		path := filepath.Join(dir, sub)
		switch _, err := os.Stat(path); {
		case err == nil:
			res.Existed = append(res.Existed, sub+"/")
		case errors.Is(err, fs.ErrNotExist):
			if merr := os.MkdirAll(path, filesystem.ModePublicDir); merr != nil {
				return res, fmt.Errorf("mkdir %q: %w", path, merr)
			}
			res.Created = append(res.Created, sub+"/")
		default:
			return res, fmt.Errorf("stat %q: %w", path, err)
		}
	}

	return res, nil
}
