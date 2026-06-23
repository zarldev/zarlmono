package code

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// GlobTool enumerates workspace paths matching a glob pattern. The output
// field selects the model-facing rendering (labelled plaintext, the default,
// or JSON); the structured entries are identical either way.
//
// Labelled output shape:
//
//	matches: 3  pattern: *.go
//	  main.go         142B
//	  pkg/foo/foo.go  256B
//
// JSON output shape:
//
//	{
//	  "pattern": "*.go",
//	  "matches": 3,
//	  "truncated": false,
//	  "entries": [
//	    {"path": "main.go",         "size": 142, "dir": false},
//	    {"path": "pkg/foo/foo.go",  "size": 256, "dir": false}
//	  ]
//	}
//
// Pattern semantics (matched by [doublestar.Match]):
//
//	*.go         — match files anywhere in the tree whose basename ends in .go
//	**/*.go      — same, but explicit (the more common spelling)
//	pkg/**/*.go  — go files anywhere under pkg/
//	pkg/agent/*  — direct children of pkg/agent (single segment)
//	**/main.go   — every main.go in the tree
//
// A bare basename pattern (no path separator) matches anywhere
// recursively. A pattern containing `/` is rooted against the
// workspace (or the `root` arg) and respects path structure literally.
type GlobTool struct {
	ws                    Workspace
	allowOutsideWorkspace bool
}

// GlobArgs is the typed argument struct shared by both glob impls.
// Field tags drive both JSON decoding and SchemaFor schema generation.
type GlobArgs struct {
	// Pattern is the doublestar-flavoured glob. See type docs for
	// semantics. Required.
	Pattern string `json:"pattern" doc:"Glob pattern. Examples: \"*.go\" (every Go file), \"**/*_test.go\" (every test file), \"pkg/agent/**\" (everything under pkg/agent), \"cmd/*/main.go\" (one-level binaries)."`
	// Root scopes the walk to a subtree (workspace-relative). Empty
	// means the whole workspace.
	Root string `json:"root,omitempty" doc:"Optional sub-tree to scope the walk to (workspace-relative). Empty = whole workspace."`
	// IncludeDirs adds directory entries to the result. Default
	// false — most callers want files only.
	IncludeDirs bool `json:"include_dirs,omitempty" doc:"Include directory entries in results. Default false (files only)."`
	// MaxResults caps the returned list. Default 200; a workspace
	// of 50k files would otherwise produce a multi-MB result.
	MaxResults int `json:"max_results,omitempty" doc:"Cap on returned matches. Default 200; raise when you genuinely want every match in a huge tree."`
	// Output selects the model-facing rendering. Empty = labelled.
	Output tools.OutputFormat `json:"output,omitempty" enum:"labeled,json" doc:"Output format: \"labeled\" (default, one match per line with size) or \"json\"."`
}

// NewGlobTool returns a JSON glob tool bound to the given workspace.
func NewGlobTool(ws Workspace, opts ...ReadOption) *GlobTool {
	var policy readPolicy
	for _, opt := range opts {
		opt(&policy)
	}
	return &GlobTool{ws: ws, allowOutsideWorkspace: policy.allowOutsideWorkspace}
}

// Definition advertises glob with pattern (required), root,
// include_dirs, max_results (default 200), and a labeled|json output
// enum; enumeration never mutates.
func (t *GlobTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name: ToolNameGlob,
		Description: "Enumerate workspace paths matching a glob pattern. Returns labelled plaintext " +
			"(one match per line with size); set output=\"json\" for {pattern, matches, truncated, " +
			"entries:[{path,size,dir}]} instead. Distinct from `ls` (non-recursive directory listing) " +
			"and `grep` (searches contents). " +
			"Bare basename patterns match anywhere recursively (`*.go` → every Go file in the tree). " +
			"Path patterns are rooted against the workspace or the `root` arg (`pkg/**/*.go`).",
		Parameters: tools.SchemaFor[GlobArgs](),
	}
}

// globDefaultMax is the default cap when the caller doesn't set
// MaxResults. 200 fits comfortably in a small-context worker's
// remaining budget for any post-glob reading the model wants to do.
// Shared by both glob impls.
const globDefaultMax = 200

// globEntry is the per-result row in the JSON payload.
type globEntry struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Dir  bool   `json:"dir,omitempty"`
}

// globPayload is the top-level shape — header fields next to the
// entries array so the model sees the truncation flag without
// having to count entries against an out-of-band cap.
type globPayload struct {
	Pattern   string      `json:"pattern"`
	Root      string      `json:"root,omitempty"`
	Matches   int         `json:"matches"`
	Truncated bool        `json:"truncated,omitempty"`
	Entries   []globEntry `json:"entries"`
}

// GlobResult is glob's structured Data: the matched entries plus the call
// inputs needed to render. A consumer (the TUI) renders from Entries
// directly instead of re-parsing a string, while the model sees String():
// labelled plaintext or the JSON payload, per the requested Output.
type GlobResult struct {
	Entries    []globEntry
	Pattern    string
	Root       string
	Truncated  bool
	MaxResults int
	Output     tools.OutputFormat
}

// String renders the model-facing form for the requested output mode.
func (r GlobResult) String() string {
	if r.Output == tools.OutputJSON {
		b, err := json.Marshal(globPayload{
			Pattern:   r.Pattern,
			Root:      r.Root,
			Matches:   len(r.Entries),
			Truncated: r.Truncated,
			Entries:   r.Entries,
		})
		if err != nil {
			return "{}"
		}
		return string(b)
	}
	return renderGlobResult(r.Entries, r.Pattern, r.Root, r.MaxResults, r.Truncated)
}

// renderGlobResult formats the matches as labelled plaintext. The
// canonical shape for labelled tool output going to the model. One
// header line carries the count and the truncation flag; one line per
// match carries the path and (for files) a human-readable size.
//
// Why labelled, not JSON: a 100-match result is ~2× the tokens in
// JSON (quotes + braces + commas) and small models misread fielded
// JSON more often than they misread labelled text. The model also
// has to re-emit paths verbatim into follow-up `read` calls; copying
// from labelled text is one literal copy, copying from JSON produces
// escape errors.
func renderGlobResult(entries []globEntry, pattern, root string, maxResults int, truncated bool) string {
	var b strings.Builder
	header := fmt.Sprintf("matches: %d", len(entries))
	if truncated {
		header += fmt.Sprintf(" (truncated at cap %d — pass a higher max_results or narrow the pattern for more)", maxResults)
	}
	header += fmt.Sprintf("  pattern: %s", pattern)
	if root != "" {
		header += fmt.Sprintf("  root: %s", root)
	}
	b.WriteString(header)
	b.WriteString("\n")
	if len(entries) == 0 {
		b.WriteString("(no matches)")
		return b.String()
	}
	for _, e := range entries {
		b.WriteString("  ")
		b.WriteString(e.Path)
		if e.Dir {
			b.WriteString("  (dir)")
		} else {
			b.WriteString("  ")
			b.WriteString(formatBytes(e.Size))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// formatBytes converts a byte count into a compact human-readable
// suffix. Lower-case units, no decimals (the model wants a sense of
// magnitude, not precision). Shared with ls for size rendering
// consistency across labelled enumeration tools.
func formatBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%dKB", (n+512)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%dMB", (n+512*1024)/(1024*1024))
	default:
		return fmt.Sprintf("%dGB", (n+512*1024*1024)/(1024*1024*1024))
	}
}

func relUnder(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", false
	}
	return rel, true
}

func walkGlobMatches(ctx context.Context, ws Workspace, args GlobArgs, rootAbs string, maxResults int) ([]globEntry, bool, error) {
	basenameOnly := !strings.ContainsRune(args.Pattern, '/')
	matches := []globEntry{}
	truncated := false

	walkRoot := rootAbs
	rootForRel := rootAbs
	if ws.OSRoot() != nil && ws.contains(rootAbs) {
		relRoot, relErr := ws.RelToRoot(rootAbs)
		if relErr != nil {
			return nil, false, tools.Permission("glob", relErr.Error())
		}
		walkRoot = relRoot
		rootForRel = relRoot
	}
	walkFn := filepath.WalkDir
	if ws.OSRoot() != nil && ws.contains(rootAbs) {
		walkFn = func(root string, fn fs.WalkDirFunc) error {
			return fs.WalkDir(ws.OSRoot().FS(), filepath.ToSlash(root), fn)
		}
	}
	walkErr := walkFn(walkRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, ok := relUnder(rootForRel, path)
		if !ok || rel == "." {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") && d.Name() != "." {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		isDir := d.IsDir()
		target := rel
		if basenameOnly {
			target = d.Name()
		}
		matched, _ := doublestar.Match(args.Pattern, target)
		if !matched {
			return nil
		}
		if isDir && !args.IncludeDirs {
			return nil
		}
		if len(matches) >= maxResults {
			truncated = true
			return filepath.SkipAll
		}
		var size int64
		if fi, statErr := d.Info(); statErr == nil {
			size = fi.Size()
		}
		matches = append(matches, globEntry{Path: rel, Size: size, Dir: isDir})
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, filepath.SkipAll) {
		return nil, false, walkErr
	}
	slices.SortFunc(matches, func(a, b globEntry) int { return cmp.Compare(a.Path, b.Path) })
	return matches, truncated, nil
}

// resolveGlob validates args, applies defaults, resolves the search
// root, and returns the absolute root path and effective maxResults.
func resolveGlob(ws Workspace, allowOutsideWorkspace bool, args GlobArgs) (string, int, error) {
	if args.Pattern == "" {
		return "", 0, tools.Validation("glob", "pattern required")
	}
	if !doublestar.ValidatePattern(args.Pattern) {
		return "", 0, tools.Validation("glob",
			fmt.Sprintf("invalid glob pattern %q — see doublestar syntax", args.Pattern))
	}
	maxResults := args.MaxResults
	if maxResults <= 0 {
		maxResults = globDefaultMax
	}

	rootRel := strings.TrimSpace(args.Root)
	if rootRel == "" {
		rootRel = "."
	}
	rootAbs, err := ws.ResolveForRead(rootRel, allowOutsideWorkspace)
	if err != nil {
		return "", 0, tools.Permission("glob", err.Error())
	}
	info, err := ws.StatPath(rootAbs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", 0, tools.NotFound("glob",
				fmt.Sprintf("%q does not exist", rootRel))
		}
		return "", 0, tools.Fatal("glob",
			fmt.Errorf("stat %q: %w", rootRel, err))
	}
	if !info.IsDir() {
		return "", 0, tools.Validation("glob",
			fmt.Sprintf("root %q is not a directory", rootRel))
	}
	return rootAbs, maxResults, nil
}

// Execute walks the workspace (or the configured sub-root) and
// returns every path matching the pattern, in lexical order, in the
// tool's configured output format (labelled text by default).
func (t *GlobTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	var args GlobArgs
	if derr := tools.DecodeArgs(call.Arguments, &args); derr != nil {
		return tools.Failure(call.ID, derr), nil
	}
	rootAbs, maxResults, err := resolveGlob(t.ws, t.allowOutsideWorkspace, args)
	if err != nil {
		return tools.Failure(call.ID, err), nil
	}

	matches, truncated, err := walkGlobMatches(ctx, t.ws, args, rootAbs, maxResults)
	if err != nil {
		return tools.Failure(call.ID, tools.Fatal("glob", fmt.Errorf("walk: %w", err))), nil
	}

	return tools.Success(call.ID, GlobResult{
		Entries:    matches,
		Pattern:    args.Pattern,
		Root:       args.Root,
		Truncated:  truncated,
		MaxResults: maxResults,
		Output:     args.Output.Resolve(),
	}), nil
}
