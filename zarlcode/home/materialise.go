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
	seeded, err := materialiseDirs(dir, []string{"skills", "tools", "hooks"})
	if err != nil {
		return res, err
	}
	res.Created = seeded.Created
	res.Existed = seeded.Existed

	return res, nil
}

// MaterialiseWorkspace ensures a workspace has empty local extension
// directories. It never creates definition files or changes existing content.
func MaterialiseWorkspace(workspaceRoot string) (Result, error) {
	dir := WorkspaceDir(workspaceRoot)
	if dir == "" {
		return Result{}, errors.New("workspace root is required")
	}
	return materialiseDirs(dir, []string{"agents", "skills", "tools", "hooks"})
}

func materialiseDirs(dir string, subdirs []string) (Result, error) {
	res := Result{Dir: dir}
	if err := os.MkdirAll(dir, filesystem.ModePublicDir); err != nil {
		return res, fmt.Errorf("mkdir %q: %w", dir, err)
	}
	for _, sub := range subdirs {
		path := filepath.Join(dir, sub)
		switch info, err := os.Stat(path); {
		case err == nil && info.IsDir():
			res.Existed = append(res.Existed, sub+"/")
		case err == nil:
			return res, fmt.Errorf("seed %q: path exists and is not a directory", path)
		case errors.Is(err, fs.ErrNotExist):
			if err := os.MkdirAll(path, filesystem.ModePublicDir); err != nil {
				return res, fmt.Errorf("mkdir %q: %w", path, err)
			}
			res.Created = append(res.Created, sub+"/")
		default:
			return res, fmt.Errorf("stat %q: %w", path, err)
		}
	}
	return res, nil
}
