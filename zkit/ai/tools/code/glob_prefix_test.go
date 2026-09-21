package code_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestGlob_LiteralPrefixSemantics(t *testing.T) {
	t.Parallel()
	for _, outside := range []bool{false, true} {
		name := "workspace"
		if outside {
			name = "unrestricted"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			seedGlobPrefixTree(t, root)
			workspaceRoot := root
			if outside {
				workspaceRoot = t.TempDir()
			}
			ws := openGlobPrefixWorkspace(t, workspaceRoot)
			g := code.NewGlobTool(ws)
			if outside {
				g = code.NewGlobTool(ws, code.WithUnrestrictedReads())
			}
			for _, tc := range []struct {
				name, pattern, root string
				includeDirs         bool
				cap                 int
				paths               []string
				truncated           bool
			}{
				{name: "recursive", pattern: "pkg/agent/**/*.go", paths: []string{"pkg/agent/a.go", "pkg/agent/b.go", "pkg/agent/nested/c.go"}},
				{name: "direct", pattern: "pkg/agent/*.go", paths: []string{"pkg/agent/a.go", "pkg/agent/b.go"}},
				{name: "exact-file", pattern: "pkg/agent/a.go", paths: []string{"pkg/agent/a.go"}},
				{name: "missing-prefix", pattern: "missing/deep/*.go"},
				{name: "file-as-prefix", pattern: "pkg/agent/a.go/*.go"},
				{name: "hidden-prefix", pattern: "pkg/.hidden/**/*.go"},
				{name: "hidden-root-prefix", pattern: ".hidden/**/*.go"},
				{name: "scoped-root", pattern: "agent/**/*.go", root: "pkg", paths: []string{"agent/a.go", "agent/b.go", "agent/nested/c.go"}},
				{name: "prefix-directory", pattern: "pkg/agent/**", includeDirs: true, paths: []string{"pkg/agent", "pkg/agent/a.go", "pkg/agent/b.go", "pkg/agent/nested", "pkg/agent/nested/c.go"}},
				{name: "exact-directory", pattern: "pkg/agent", includeDirs: true, paths: []string{"pkg/agent"}},
				{name: "exact-cap", pattern: "pkg/agent/*.go", cap: 2, paths: []string{"pkg/agent/a.go", "pkg/agent/b.go"}},
				{name: "truncated", pattern: "pkg/agent/**/*.go", cap: 2, paths: []string{"pkg/agent/a.go", "pkg/agent/b.go"}, truncated: true},
				{name: "directory-cap", pattern: "pkg/agent/**", includeDirs: true, cap: 1, paths: []string{"pkg/agent"}, truncated: true},
				{name: "wildcard-segment", pattern: "pkg/agent*/*.go", paths: []string{"pkg/agent-extra/extra.go", "pkg/agent/a.go", "pkg/agent/b.go"}},
				{name: "question-mark", pattern: "pkg/agen?/*.go", paths: []string{"pkg/agent/a.go", "pkg/agent/b.go"}},
				{name: "character-class", pattern: "pkg/agen[t]/*.go", paths: []string{"pkg/agent/a.go", "pkg/agent/b.go"}},
				{name: "alternatives", pattern: "{pkg/agent,other}/**/*.go", paths: []string{"other/main.go", "pkg/agent/a.go", "pkg/agent/b.go", "pkg/agent/nested/c.go"}},
				{name: "nested-alternatives", pattern: "pkg/{agent,other}/**/*.go", paths: []string{"pkg/agent/a.go", "pkg/agent/b.go", "pkg/agent/nested/c.go", "pkg/other/other.go"}},
				{name: "escaped-prefix", pattern: `literal\[dir\]/*.go`, paths: []string{"literal[dir]/file.go"}},
				{name: "escape-after-prefix", pattern: `pkg/literal\{dir\}/*.go`, paths: []string{"pkg/literal{dir}/file.go"}},
				{name: "unicode-prefix", pattern: "pkg/日本/*.go", paths: []string{"pkg/日本/a.go"}},
				{name: "noncanonical-prefix", pattern: "pkg/agent/../other/*.go"},
				{name: "double-separator", pattern: "pkg//agent/*.go"},
				{name: "leading-dot", pattern: "./pkg/agent/*.go"},
				{name: "basename-fallback", pattern: "a.go", paths: []string{"pkg/agent/a.go", "pkg/日本/a.go"}},
				{name: "recursive-fallback", pattern: "**/c.go", paths: []string{"pkg/agent/nested/c.go"}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					searchRoot := tc.root
					if outside {
						searchRoot = filepath.Join(root, tc.root)
					}
					call := globCall(map[string]any{
						"pattern": tc.pattern, "root": searchRoot,
						"include_dirs": tc.includeDirs, "max_results": tc.cap,
					})
					res, err := g.Execute(t.Context(), call)
					if err != nil || res == nil || !res.Success {
						t.Fatalf("Execute: result=%+v err=%v", res, err)
					}
					got, ok := res.Data.(code.GlobResult)
					if !ok {
						t.Fatalf("Data is %T, want code.GlobResult", res.Data)
					}
					var paths []string
					for _, entry := range got.Entries {
						paths = append(paths, entry.Path)
					}
					if !slices.Equal(paths, tc.paths) || got.Truncated != tc.truncated {
						t.Errorf("paths=%v truncated=%v; want paths=%v truncated=%v", paths, got.Truncated, tc.paths, tc.truncated)
					}
					if got.Pattern != tc.pattern || got.Root != searchRoot {
						t.Errorf("result changed inputs: pattern=%q root=%q", got.Pattern, got.Root)
					}
				})
			}
		})
	}
}

func TestGlob_LiteralPrefixDoesNotTraverseSymlinks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	seedGlobPrefixTree(t, root)
	outside := t.TempDir()
	seedGlobPrefixTree(t, outside)
	for name, target := range map[string]string{"inside-link": filepath.Join(root, "pkg"), "outside-link": outside} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	g := code.NewGlobTool(openGlobPrefixWorkspace(t, root))
	for _, pattern := range []string{"inside-link/agent/**/*.go", "outside-link/pkg/agent/**/*.go"} {
		res, err := g.Execute(t.Context(), globCall(map[string]any{"pattern": pattern}))
		if err != nil || res == nil || !res.Success {
			t.Fatalf("Execute %q: result=%+v err=%v", pattern, res, err)
		}
		if got := res.Data.(code.GlobResult); len(got.Entries) != 0 || got.Truncated {
			t.Errorf("followed symlink for %q: %+v", pattern, got)
		}
	}
}

func TestGlob_LiteralPrefixCancellation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	seedGlobPrefixTree(t, root)
	g := code.NewGlobTool(openGlobPrefixWorkspace(t, root))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	res, err := g.Execute(ctx, globCall(map[string]any{"pattern": "missing/prefix/**/*.go"}))
	if err != nil || res == nil || res.Success || !errors.Is(res.Err, context.Canceled) {
		t.Fatalf("canceled Execute: result=%+v err=%v", res, err)
	}
}

func seedGlobPrefixTree(t testing.TB, root string) {
	t.Helper()
	for _, name := range []string{
		"pkg/agent/a.go", "pkg/agent/b.go", "pkg/agent/nested/c.go",
		"pkg/agent/.hidden/x.go", "pkg/.hidden/x.go", ".hidden/x.go",
		"pkg/agent-extra/extra.go", "pkg/other/other.go", "other/main.go",
		"literal[dir]/file.go", "pkg/literal{dir}/file.go", "pkg/日本/a.go",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("package fixture\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func openGlobPrefixWorkspace(t testing.TB, root string) code.Workspace {
	t.Helper()
	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := ws.Close(); err != nil {
			t.Error(err)
		}
	})
	return ws
}

func BenchmarkGlobLiteralPrefix(b *testing.B) {
	root := b.TempDir()
	seedGlobPrefixTree(b, root)
	// Unrelated subtrees dominate full traversal, like dependency trees in a
	// monorepo. Both patterns return the same files; the brace form deliberately
	// exercises the conservative full-walk fallback.
	for i := range 200 {
		dir := filepath.Join(root, "unrelated", fmt.Sprintf("dir-%03d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatal(err)
		}
		for j := range 10 {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-%02d.go", j)), nil, 0o644); err != nil {
				b.Fatal(err)
			}
		}
	}
	g := code.NewGlobTool(openGlobPrefixWorkspace(b, root))
	for _, tc := range []struct{ name, pattern string }{
		{"literal", "pkg/agent/**/*.go"},
		{"full-walk-fallback", "{pkg}/agent/**/*.go"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			call := tools.ToolCall{ToolName: code.ToolNameGlob, Arguments: tools.ToolParameters{"pattern": tc.pattern}}
			b.ReportAllocs()
			for b.Loop() {
				res, err := g.Execute(b.Context(), call)
				if err != nil || res == nil || !res.Success {
					b.Fatalf("Execute: result=%+v err=%v", res, err)
				}
			}
		})
	}
}
