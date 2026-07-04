package instructions

import (
	"bytes"
	"cmp"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	// DefaultMaxBytes is the total instruction body budget included in prompts.
	DefaultMaxBytes = 32 * 1024
)

var instructionNames = map[string]bool{
	"AGENTS.md": true,
	"CLAUDE.md": true,
}

var ignoredDirNames = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	"coverage":     true,
}

// Document is one repository instruction file discovered under a workspace.
type Document struct {
	Path      string // absolute path
	RelPath   string // workspace-relative, slash-separated path
	Name      string // AGENTS.md or CLAUDE.md
	Content   string
	Truncated bool
}

// NestedDoc is a lightweight index entry for a non-root instruction file
// discovered below the workspace root. Callers enumerate these via list_instructions
// and load full bodies on demand via load_instruction.
type NestedDoc struct {
	RelPath string // workspace-relative, slash-separated path
}

// Discover walks workspaceRoot for AGENTS.md and CLAUDE.md instruction files.
// Individual unreadable or non-text files are reported in the returned error
// slice without hiding other valid documents.
func Discover(workspaceRoot string, maxBytes int) ([]Document, []error) {
	if maxBytes < 0 {
		maxBytes = 0
	}
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, []error{fmt.Errorf("resolve workspace root %q: %w", workspaceRoot, err)}
	}

	var paths []string
	var errs []error
	if err := filepath.WalkDir(root, func(path string, ent fs.DirEntry, err error) error {
		if err != nil {
			errs = append(errs, fmt.Errorf("walk %q: %w", path, err))
			if ent != nil && ent.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if ent.IsDir() {
			if shouldSkipDir(root, path, ent.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if instructionNames[ent.Name()] {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("walk workspace instructions: %w", err))
	}

	sortInstructionPaths(root, paths)

	return readDocuments(root, paths, maxBytes)
}

// DiscoverRoot is like Discover but returns only the instruction files directly in the
// workspace root directory. The maxBytes cap applies to their combined body size.
// Nested instruction files are discoverable via ListNested and loadable via LoadOne.
func DiscoverRoot(workspaceRoot string, maxBytes int) ([]Document, []error) {
	if maxBytes < 0 {
		maxBytes = 0
	}
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, []error{fmt.Errorf("resolve workspace root %q: %w", workspaceRoot, err)}
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, []error{fmt.Errorf("read workspace root %q: %w", root, err)}
	}

	var paths []string
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		if instructionNames[ent.Name()] {
			paths = append(paths, filepath.Join(root, ent.Name()))
		}
	}
	sortInstructionPaths(root, paths)

	return readDocuments(root, paths, maxBytes)
}

// ListNested returns an index of instruction files below the workspace root without
// reading file bodies. Paths are sorted depth-first, root-first within depth.
func ListNested(workspaceRoot string) ([]NestedDoc, []error) {
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, []error{fmt.Errorf("resolve workspace root %q: %w", workspaceRoot, err)}
	}

	var paths []string
	var errs []error
	if err := filepath.WalkDir(root, func(path string, ent fs.DirEntry, err error) error {
		if err != nil {
			errs = append(errs, fmt.Errorf("walk %q: %w", path, err))
			if ent != nil && ent.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if ent.IsDir() {
			if shouldSkipDir(root, path, ent.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if instructionNames[ent.Name()] {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				errs = append(errs, fmt.Errorf("rel %q: %w", path, err))
				return nil
			}
			// Skip root-level — those are the eager DiscoverRoot set.
			if strings.Count(rel, string(filepath.Separator)) == 0 {
				return nil
			}
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("walk nested instructions: %w", err))
	}

	sortInstructionPaths(root, paths)

	docs := make([]NestedDoc, 0, len(paths))
	for _, path := range paths {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			errs = append(errs, fmt.Errorf("rel nested instruction %q: %w", path, err))
			continue
		}
		docs = append(docs, NestedDoc{RelPath: filepath.ToSlash(rel)})
	}
	return docs, errs
}

// LoadOne reads a single instruction file by its workspace-relative path and returns
// its full [Document]. relPath must be a path previously returned by [ListNested] or
// [Discover].
func LoadOne(workspaceRoot, relPath string) (Document, error) {
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return Document{}, fmt.Errorf("resolve workspace root %q: %w", workspaceRoot, err)
	}
	clean := filepath.Clean(relPath)
	if filepath.IsAbs(clean) {
		return Document{}, fmt.Errorf("relPath must be relative: %q", relPath)
	}
	if strings.Contains(clean, "..") {
		return Document{}, fmt.Errorf("relPath must not escape workspace: %q", relPath)
	}
	abs := filepath.Join(root, clean)
	// Verify abs is still under root.
	relCheck, err := filepath.Rel(root, abs)
	if err != nil || strings.HasPrefix(relCheck, "..") {
		return Document{}, fmt.Errorf("relPath escapes workspace: %q", relPath)
	}

	body, err := os.ReadFile(abs)
	if err != nil {
		return Document{}, fmt.Errorf("read instruction %q: %w", abs, err)
	}
	if !utf8.Valid(body) {
		return Document{}, fmt.Errorf("read instruction %q: not valid UTF-8 text", abs)
	}
	if bytes.ContainsRune(body, '\x00') {
		return Document{}, fmt.Errorf("read instruction %q: not text", abs)
	}

	return Document{
		Path:      abs,
		RelPath:   filepath.ToSlash(clean),
		Name:      filepath.Base(abs),
		Content:   strings.TrimSpace(string(body)),
		Truncated: false,
	}, nil
}

// readDocuments reads and validates instruction files at the given absolute paths,
// applying the byte budget. It is shared by Discover and DiscoverRoot.
func readDocuments(root string, paths []string, maxBytes int) ([]Document, []error) {
	var errs []error
	docs := make([]Document, 0, len(paths))
	remaining := maxBytes
	for i, path := range paths {
		if maxBytes > 0 && remaining <= 0 {
			markCapReached(docs, len(paths)-i)
			break
		}
		body, err := os.ReadFile(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("read instruction %q: %w", path, err))
			continue
		}
		if !utf8.Valid(body) {
			errs = append(errs, fmt.Errorf("read instruction %q: not valid UTF-8 text", path))
			continue
		}
		if bytes.ContainsRune(body, '\x00') {
			errs = append(errs, fmt.Errorf("read instruction %q: not text", path))
			continue
		}

		content := string(body)
		truncated := false
		if maxBytes > 0 && len(body) > remaining {
			content = string(body[:validUTF8PrefixLen(body, remaining)])
			content = strings.TrimRight(content, "\r\n") + "\n\n[... truncated: workspace instruction byte cap reached ...]"
			truncated = true
			remaining = 0
		} else {
			remaining -= len(body)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			errs = append(errs, fmt.Errorf("rel instruction %q: %w", path, err))
			rel = filepath.Base(path)
		}
		docs = append(docs, Document{
			Path:      path,
			RelPath:   filepath.ToSlash(rel),
			Name:      filepath.Base(path),
			Content:   strings.TrimSpace(content),
			Truncated: truncated,
		})
	}
	return docs, errs
}

func markCapReached(docs []Document, omitted int) {
	if len(docs) == 0 || omitted <= 0 {
		return
	}
	last := &docs[len(docs)-1]
	last.Truncated = true
	last.Content = strings.TrimRight(last.Content, "\r\n") + fmt.Sprintf("\n\n[... truncated: workspace instruction byte cap reached; %d more file(s) omitted ...]", omitted)
}

func validUTF8PrefixLen(body []byte, n int) int {
	if n >= len(body) {
		return len(body)
	}
	for n > 0 && !utf8.Valid(body[:n]) {
		n--
	}
	return n
}

func shouldSkipDir(root, path, name string) bool {
	if path == root {
		return false
	}
	if ignoredDirNames[name] {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	return rel == ".zarlcode/sessions" || strings.Contains(rel, "/.zarlcode/sessions/")
}

func sortInstructionPaths(root string, paths []string) {
	slices.SortFunc(paths, func(a, b string) int {
		ai := sortKey(root, a)
		aj := sortKey(root, b)
		if ai.depth != aj.depth {
			return cmp.Compare(ai.depth, aj.depth)
		}
		if ai.dir != aj.dir {
			return cmp.Compare(ai.dir, aj.dir)
		}
		if ai.nameRank != aj.nameRank {
			return cmp.Compare(ai.nameRank, aj.nameRank)
		}
		return cmp.Compare(ai.rel, aj.rel)
	})
}

type instructionSortKey struct {
	rel      string
	dir      string
	depth    int
	nameRank int
}

func sortKey(root, path string) instructionSortKey {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	rel = filepath.ToSlash(rel)
	dir := filepath.ToSlash(filepath.Dir(rel))
	if dir == "." {
		dir = ""
	}
	depth := 0
	if dir != "" {
		depth = strings.Count(dir, "/") + 1
	}
	return instructionSortKey{
		rel:      rel,
		dir:      dir,
		depth:    depth,
		nameRank: nameRank(filepath.Base(path)),
	}
}

func nameRank(name string) int {
	switch name {
	case "AGENTS.md":
		return 0
	case "CLAUDE.md":
		return 1
	default:
		return 2
	}
}
