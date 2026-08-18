package instructions

import (
	"path"
	"slices"
	"strings"
)

// Index is an immutable lookup from workspace-relative paths to nested
// instruction documents that govern them. Root instruction files are excluded
// because callers already include them eagerly in the system prompt.
type Index struct {
	entries []indexEntry
}

type indexEntry struct {
	dir     string
	relPath string
	depth   int
}

// NewIndex builds an instruction index from a ListNested snapshot.
func NewIndex(docs []NestedDoc) Index {
	entries := make([]indexEntry, 0, len(docs))
	seen := make(map[string]struct{}, len(docs))
	for _, doc := range docs {
		rel, ok := cleanRelativePath(doc.RelPath)
		if !ok || !strings.Contains(rel, "/") {
			continue
		}
		if _, exists := seen[rel]; exists {
			continue
		}
		seen[rel] = struct{}{}
		dir := path.Dir(rel)
		entries = append(entries, indexEntry{
			dir:     dir,
			relPath: rel,
			depth:   strings.Count(dir, "/") + 1,
		})
	}
	slices.SortFunc(entries, func(a, b indexEntry) int {
		if a.depth != b.depth {
			return b.depth - a.depth
		}
		return strings.Compare(a.relPath, b.relPath)
	})
	return Index{entries: entries}
}

// Applicable returns nested instruction paths governing any of paths. Nearest
// documents come first, duplicate documents are returned once, and the result
// is independent of the index's internal storage.
func (i Index) Applicable(paths ...string) []string {
	cleaned := make([]string, 0, len(paths))
	for _, candidate := range paths {
		if rel, ok := cleanRelativePath(candidate); ok {
			cleaned = append(cleaned, rel)
		}
	}
	if len(cleaned) == 0 {
		return nil
	}

	out := make([]string, 0)
	seen := make(map[string]struct{})
	for _, entry := range i.entries {
		for _, candidate := range cleaned {
			if candidate != entry.dir && !strings.HasPrefix(candidate, entry.dir+"/") {
				continue
			}
			if _, exists := seen[entry.relPath]; !exists {
				seen[entry.relPath] = struct{}{}
				out = append(out, entry.relPath)
			}
			break
		}
	}
	return out
}

func cleanRelativePath(value string) (string, bool) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" || strings.HasPrefix(value, "/") {
		return "", false
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	return cleaned, true
}
