package code_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// globHarness builds a workspace populated with the given files and
// returns a ready-to-call GlobTool.
func globHarness(t *testing.T, files map[string]string) *code.GlobTool {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", abs, err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", abs, err)
		}
	}
	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	return code.NewGlobTool(ws)
}

func globCall(args map[string]any) tools.ToolCall {
	return tools.ToolCall{
		ID:        "c",
		ToolName:  code.ToolNameGlob,
		Arguments: tools.ToolParameters(args),
	}
}

// globText renders the model-facing string for a result — the same text
// the runner flattens GlobResult to via fmt.Stringer.
func globText(t *testing.T, res *tools.ToolResult) string {
	t.Helper()
	r, ok := res.Data.(code.GlobResult)
	if !ok {
		t.Fatalf("Data is %T, want code.GlobResult", res.Data)
	}
	return r.String()
}

// jsonPayload is the parsed shape the JSON output renders — local to the
// test so we're not coupled to the unexported globPayload type.
type jsonPayload struct {
	Pattern   string `json:"pattern"`
	Root      string `json:"root,omitempty"`
	Matches   int    `json:"matches"`
	Truncated bool   `json:"truncated,omitempty"`
	Entries   []struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
		Dir  bool   `json:"dir,omitempty"`
	} `json:"entries"`
}

func decodePayload(t *testing.T, body string) jsonPayload {
	t.Helper()
	var p jsonPayload
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("decode: %v\nbody: %q", err, body)
	}
	return p
}

// --- default (labelled) output ---------------------------------------

// Bare basename pattern matches anywhere recursively — the model's
// intuition for "every Go file".
func TestGlob_BasenamePatternMatchesRecursively(t *testing.T) {
	t.Parallel()
	g := globHarness(t, map[string]string{
		"main.go":          "package main",
		"pkg/foo/foo.go":   "package foo",
		"pkg/bar/bar.go":   "package bar",
		"docs/readme.md":   "# docs",
		"pkg/foo/notes.md": "notes",
	})
	res, _ := g.Execute(t.Context(), globCall(map[string]any{"pattern": "*.go"}))
	if res == nil || !res.Success {
		t.Fatalf("Execute: %+v", res)
	}
	body := globText(t, res)
	for _, want := range []string{"main.go", "pkg/foo/foo.go", "pkg/bar/bar.go"} {
		if !strings.Contains(body, want) {
			t.Errorf("output missing %q in: %q", want, body)
		}
	}
	for _, unwanted := range []string{"readme.md", "notes.md"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("output contains non-matching %q: %q", unwanted, body)
		}
	}
	if !strings.Contains(body, "matches: 3") {
		t.Errorf("output missing count header: %q", body)
	}
}

// A path-style pattern is rooted at the workspace and respects path
// structure literally — pkg/agent/**/*.go means "Go files anywhere
// under pkg/agent".
func TestGlob_PathPatternIsRooted(t *testing.T) {
	t.Parallel()
	g := globHarness(t, map[string]string{
		"pkg/agent/run.go": "1",
		"pkg/agent/x/y.go": "2",
		"pkg/other/run.go": "3",
		"cmd/main.go":      "4",
	})
	res, _ := g.Execute(t.Context(), globCall(map[string]any{"pattern": "pkg/agent/**/*.go"}))
	if res == nil || !res.Success {
		t.Fatalf("Execute: %+v", res)
	}
	body := globText(t, res)
	if !strings.Contains(body, "pkg/agent/x/y.go") {
		t.Errorf("nested match missing: %q", body)
	}
	if strings.Contains(body, "pkg/other") || strings.Contains(body, "cmd/main.go") {
		t.Errorf("non-matching paths leaked: %q", body)
	}
}

// Dotfiles + dotdirs are excluded by default. We never want to flood
// the model's context with .git contents.
func TestGlob_ExcludesDotfilesByDefault(t *testing.T) {
	t.Parallel()
	g := globHarness(t, map[string]string{
		"main.go":        "1",
		".gitignore":     "node_modules",
		".git/HEAD":      "ref",
		"docs/.draft.md": "draft",
	})
	res, _ := g.Execute(t.Context(), globCall(map[string]any{"pattern": "**"}))
	body := globText(t, res)
	if strings.Contains(body, ".git") || strings.Contains(body, ".draft") || strings.Contains(body, ".gitignore") {
		t.Errorf("dotfile leaked: %q", body)
	}
	if !strings.Contains(body, "main.go") {
		t.Errorf("main.go missing: %q", body)
	}
}

// Default output is labelled, not JSON: no braces, no quotes, one match
// per line. Canonical reference for the labelled-output convention; if
// this starts asserting JSON shapes the convention has slipped.
func TestGlob_DefaultIsLabelledNotJSON(t *testing.T) {
	t.Parallel()
	g := globHarness(t, map[string]string{"a.go": "x"})
	res, _ := g.Execute(t.Context(), globCall(map[string]any{"pattern": "*.go"}))
	body := globText(t, res)
	for _, forbid := range []string{"{", "}", "[", "]", "\":\"", "\",\"", "\"a.go\""} {
		if strings.Contains(body, forbid) {
			t.Errorf("output contains JSON-shaped %q (must be labelled plaintext): %q", forbid, body)
		}
	}
	if !strings.HasPrefix(body, "matches: ") {
		t.Errorf("output missing labelled count header: %q", body)
	}
	if !strings.Contains(body, "\n  a.go") {
		t.Errorf("match row not indented as expected: %q", body)
	}
}

func TestGlob_MaxResultsTruncatesAndAnnounces(t *testing.T) {
	t.Parallel()
	files := map[string]string{}
	for i := 'a'; i <= 'z'; i++ {
		files[string(i)+".go"] = "x"
	}
	g := globHarness(t, files)
	res, _ := g.Execute(t.Context(), globCall(map[string]any{
		"pattern":     "*.go",
		"max_results": 5,
	}))
	body := globText(t, res)
	if !strings.Contains(body, "matches: 5") {
		t.Errorf("count header should report 5: %q", body)
	}
	if !strings.Contains(body, "truncated at cap 5") {
		t.Errorf("output should announce truncation: %q", body)
	}
}

func TestGlob_EmptyPatternRejected(t *testing.T) {
	t.Parallel()
	g := globHarness(t, nil)
	res, _ := g.Execute(t.Context(), globCall(map[string]any{"pattern": ""}))
	if res == nil || res.Success {
		t.Fatalf("empty pattern: want failure, got %+v", res)
	}
	if !strings.Contains(res.Error, "pattern required") {
		t.Errorf("error should say pattern required: %q", res.Error)
	}
}

// No matches → header + "(no matches)" sentinel. The model must be
// able to distinguish "zero matches" from "tool errored".
func TestGlob_NoMatchesSentinel(t *testing.T) {
	t.Parallel()
	g := globHarness(t, map[string]string{"a.go": "x"})
	res, _ := g.Execute(t.Context(), globCall(map[string]any{"pattern": "*.nonsense"}))
	body := globText(t, res)
	if !res.Success {
		t.Fatalf("want success even with zero matches, got %+v", res)
	}
	if !strings.Contains(body, "matches: 0") {
		t.Errorf("count header missing: %q", body)
	}
	if !strings.Contains(body, "(no matches)") {
		t.Errorf("no-matches sentinel missing: %q", body)
	}
}

func TestGlob_RootScopesTheWalk(t *testing.T) {
	t.Parallel()
	g := globHarness(t, map[string]string{
		"pkg/agent/x_test.go": "1",
		"pkg/other/y_test.go": "2",
	})
	res, _ := g.Execute(t.Context(), globCall(map[string]any{
		"pattern": "*_test.go",
		"root":    "pkg/agent",
	}))
	body := globText(t, res)
	if !strings.Contains(body, "x_test.go") {
		t.Errorf("x_test.go missing: %q", body)
	}
	if strings.Contains(body, "y_test.go") {
		t.Errorf("y_test.go leaked from outside root: %q", body)
	}
}

func TestGlob_IncludeDirsAddsDirectoryEntries(t *testing.T) {
	t.Parallel()
	g := globHarness(t, map[string]string{
		"pkg/agent/run.go": "x",
		"pkg/other/y.go":   "x",
		"cmd/main.go":      "x",
	})
	res, _ := g.Execute(t.Context(), globCall(map[string]any{
		"pattern":      "pkg/*",
		"include_dirs": true,
	}))
	body := globText(t, res)
	for _, want := range []string{"pkg/agent", "pkg/other", "(dir)"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %q in output: %q", want, body)
		}
	}
}

// --- JSON output ------------------------------------------------------

// output="json" renders the top-level {pattern, matches, entries} object
// (not an array) — same matches, different String().
func TestGlob_JSONOutputShape(t *testing.T) {
	t.Parallel()
	g := globHarness(t, map[string]string{
		"main.go":        "x",
		"pkg/foo/foo.go": "x",
		"docs/readme.md": "x",
	})
	res, _ := g.Execute(t.Context(), globCall(map[string]any{
		"pattern": "*.go",
		"output":  "json",
	}))
	if res == nil || !res.Success {
		t.Fatalf("Execute: %+v", res)
	}
	body := globText(t, res)
	if !strings.HasPrefix(body, "{") {
		t.Errorf("JSON output should start with `{`: %q", body)
	}
	if strings.HasPrefix(body, "matches:") {
		t.Errorf("output looks labelled, want JSON: %q", body)
	}
	p := decodePayload(t, body)
	if p.Pattern != "*.go" {
		t.Errorf("pattern = %q, want *.go", p.Pattern)
	}
	if p.Matches != 2 {
		t.Errorf("matches = %d, want 2", p.Matches)
	}
	paths := map[string]bool{}
	for _, e := range p.Entries {
		paths[e.Path] = true
	}
	for _, want := range []string{"main.go", "pkg/foo/foo.go"} {
		if !paths[want] {
			t.Errorf("entry %q missing", want)
		}
	}
}

func TestGlob_JSONTruncatedFlag(t *testing.T) {
	t.Parallel()
	files := map[string]string{}
	for i := 'a'; i <= 'z'; i++ {
		files[string(i)+".go"] = "x"
	}
	g := globHarness(t, files)
	res, _ := g.Execute(t.Context(), globCall(map[string]any{
		"pattern": "*.go", "max_results": 5, "output": "json",
	}))
	p := decodePayload(t, globText(t, res))
	if p.Matches != 5 {
		t.Errorf("matches = %d, want 5", p.Matches)
	}
	if !p.Truncated {
		t.Errorf("truncated flag should be true when cap is hit")
	}
}

// The structured Data exposes typed entries regardless of output mode.
func TestGlob_DataCarriesTypedEntries(t *testing.T) {
	t.Parallel()
	g := globHarness(t, map[string]string{"main.go": "x", "pkg/foo.go": "x"})
	res, _ := g.Execute(t.Context(), globCall(map[string]any{"pattern": "*.go"}))
	r, ok := res.Data.(code.GlobResult)
	if !ok {
		t.Fatalf("Data is %T, want code.GlobResult", res.Data)
	}
	if len(r.Entries) != 2 {
		t.Fatalf("want 2 typed entries, got %d", len(r.Entries))
	}
}
