package home

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/zarldev/zarlmono/zarlcode/prompts"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/filesystem"
)

// RootPromptFile is the canonical user-editable agent definition.
// The shell reads it (via the prompt loader) instead of the embedded
// system.md once it exists. Users evolve their agent by editing
// this file; the binary's embedded copy only serves as the
// first-run seed and as the fallback when the disk file matches it
// byte-for-byte (stale-untouched detection).
const RootPromptFile = "prompt.md"

// RootPromptPath returns the absolute path of the user-editable agent
// definition (~/.zarlcode/prompt.md). The prompt loader reads it when present
// and falls back to the embedded default otherwise.
func RootPromptPath() (string, error) {
	dir, err := db.DefaultDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(dir, RootPromptFile), nil
}

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
// directory layout (prompt.md, skills/, tools/, hooks/) plus a fresh
// state.db (created lazily by the first store Open). Idempotent —
// existing files are left exactly as they are.
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

	// Seed prompt.md from the embedded system prompt when missing.
	promptPath := filepath.Join(dir, RootPromptFile)
	switch _, err := os.Stat(promptPath); {
	case err == nil:
		res.Existed = append(res.Existed, RootPromptFile)
	case errors.Is(err, fs.ErrNotExist):
		if werr := os.WriteFile(promptPath, []byte(prompts.System), filesystem.ModePublicFile); werr != nil {
			return res, fmt.Errorf("write %q: %w", promptPath, werr)
		}
		res.Created = append(res.Created, RootPromptFile)
	default:
		return res, fmt.Errorf("stat %q: %w", promptPath, err)
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
