package doccheck_test

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	quickstartBegin = "<!-- quickstart:begin -->\n```go\n"
	quickstartEnd   = "\n```\n<!-- quickstart:end -->"
)

func TestQuickstartMatchesCompiledExample(t *testing.T) {
	root := filepath.Join("..", "..")
	example := readFile(t, filepath.Join(root, "examples", "quickstart", "main.go"))

	for _, path := range []string{
		filepath.Join(root, "README.md"),
		filepath.Join(root, "site", "src", "content", "docs", "getting-started.md"),
	} {
		t.Run(filepath.ToSlash(path), func(t *testing.T) {
			doc := readFile(t, path)
			snippet := markedSnippet(t, doc, quickstartBegin, quickstartEnd)
			if !bytes.Equal(bytes.TrimSpace(snippet), bytes.TrimSpace(example)) {
				t.Fatalf("quickstart differs from %s", filepath.ToSlash(filepath.Join("examples", "quickstart", "main.go")))
			}
		})
	}
}

func TestSiteRunnerRunSnippetsUseTaskResult(t *testing.T) {
	root := filepath.Join("..", "..", "site", "src", "content", "docs")
	staleAssignment := regexp.MustCompile(`(?m)\b(?:[[:word:]]+|_)\s*,\s*err\s*:=\s*[^\n]*\.Run\(`)

	walkDocs(t, root, func(path string, body []byte) {
		if bytes.Contains(body, []byte("Run(ctx context.Context, spec TaskSpec) (TaskResult, error)")) || staleAssignment.Match(body) {
			t.Errorf("%s documents the removed two-value Runner.Run contract", filepath.ToSlash(path))
		}
	})
}

func TestSiteInternalLinksResolve(t *testing.T) {
	root := filepath.Join("..", "..")
	docsRoot := filepath.Join(root, "site", "src", "content", "docs")
	link := regexp.MustCompile(`\[[^]]*\]\((/zarlmono/[^)[:space:]]*)\)`)

	walkDocs(t, docsRoot, func(path string, body []byte) {
		for _, match := range link.FindAllSubmatch(body, -1) {
			target := strings.SplitN(string(match[1]), "#", 2)[0]
			target = strings.SplitN(target, "?", 2)[0]
			rel := strings.Trim(strings.TrimPrefix(target, "/zarlmono/"), "/")
			if rel == "" {
				rel = "index"
			}
			if siteTargetExists(root, rel) {
				continue
			}
			t.Errorf("%s links to missing internal target %s", filepath.ToSlash(path), string(match[1]))
		}
	})
}

func markedSnippet(t *testing.T, body []byte, begin, end string) []byte {
	t.Helper()
	_, after, ok := bytes.Cut(body, []byte(begin))
	if !ok {
		t.Fatalf("document is missing %q", begin)
	}
	snippet, _, ok := bytes.Cut(after, []byte(end))
	if !ok {
		t.Fatalf("document is missing %q", end)
	}
	return snippet
}

func siteTargetExists(root, rel string) bool {
	candidates := []string{
		filepath.Join(root, "site", "src", "content", "docs", filepath.FromSlash(rel)+".md"),
		filepath.Join(root, "site", "src", "content", "docs", filepath.FromSlash(rel)+".mdx"),
		filepath.Join(root, "site", "public", filepath.FromSlash(rel)),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return true
		}
	}
	return false
}

func walkDocs(t *testing.T, root string, check func(string, []byte)) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || (filepath.Ext(path) != ".md" && filepath.Ext(path) != ".mdx") {
			return nil
		}
		check(path, readFile(t, path))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
