// Package code holds the file-editing tools used by the coder profile.
// All path-taking tools share a Workspace value that enforces a root
// boundary — no tool in this package may touch paths outside that root.
package code

import zkcode "github.com/zarldev/zarlmono/zkit/ai/tools/code"

// Workspace is an alias for the canonical workspace type from zkit.
// It enforces a filesystem root boundary with TOCTOU hardening:
// file operations go through an [os.Root] handle so the kernel itself
// refuses to follow symlinks out of the root even if a directory is
// swapped between Resolve and the actual open.
type Workspace = zkcode.Workspace

// NewWorkspace is an alias for the canonical constructor from zkit.
var NewWorkspace = zkcode.NewWorkspace
