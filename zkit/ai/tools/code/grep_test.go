package code_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// grepHarness builds a workspace with the given files and returns a
// ready-to-call GrepTool. Skips the suite if rg isn't on PATH so CI
// environments without ripgrep don't fail the whole package.
func grepHarness(t *testing.T, files map[string]string) *code.GrepTool {
	t.Helper()
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skipf("ripgrep not installed: %v", err)
	}
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
	return code.NewGrepTool(ws)
}

func grepCall(args map[string]any) tools.ToolCall {
	return tools.ToolCall{
		ID:        "c",
		ToolName:  code.ToolNameGrep,
		Arguments: tools.ToolParameters(args),
	}
}

// grepText renders the model-facing string for a result — the same text
// the runner flattens GrepResult to via fmt.Stringer.
func grepText(t *testing.T, res *tools.ToolResult) string {
	t.Helper()
	r, ok := res.Data.(code.GrepResult)
	if !ok {
		t.Fatalf("Data is %T, want code.GrepResult", res.Data)
	}
	return r.String()
}

// Canonical reference for the default labelled grep shape: header line,
// file path indented 2 spaces, each hit indented 4 spaces as
// `LINE: text`. If this starts asserting JSON the convention has
// slipped.
func TestGrep_DefaultGroupsByFile(t *testing.T) {
	t.Parallel()
	g := grepHarness(t, map[string]string{
		"a.go": "package a\n\nfunc Hello() string { return \"hi\" }\n",
		"b.go": "package b\n\n// Hello world\nvar x = \"hi\"\n",
	})
	res, _ := g.Execute(context.Background(), grepCall(map[string]any{
		"pattern": "Hello",
	}))
	if res == nil || !res.Success {
		t.Fatalf("Execute: %+v", res)
	}
	body := grepText(t, res)
	// Matched lines can legitimately contain braces / quotes (they're
	// source code), so the JSON-vs-labelled check is structural:
	// JSON form is a single line starting with `[`; labelled form is
	// multi-line starting with `matches:`.
	if strings.HasPrefix(body, "[") || strings.HasPrefix(body, "{") {
		t.Errorf("default output looks JSON-shaped (starts with bracket): %q", body)
	}
	if !strings.HasPrefix(body, "matches: ") {
		t.Errorf("output missing labelled count header: %q", body)
	}
	if !strings.Contains(body, "\n") {
		t.Errorf("labelled output must be multi-line, got single line: %q", body)
	}
	if !strings.Contains(body, "  a.go\n") {
		t.Errorf("file path should be on its own line indented 2 spaces: %q", body)
	}
	if !strings.Contains(body, "    3: ") && !strings.Contains(body, "    1: ") {
		t.Errorf("hit lines should be indented 4 spaces as `LINE: text`: %q", body)
	}
	// Same-file matches must not repeat the file header — that's
	// where the token win comes from.
	if strings.Count(body, "  a.go\n") > 1 {
		t.Errorf("file header should appear once per file group: %q", body)
	}
}

// output="json" switches the model-facing rendering to a JSON array of
// {file, line, text} — same structured hits, different String().
func TestGrep_JSONOutput(t *testing.T) {
	t.Parallel()
	g := grepHarness(t, map[string]string{
		"a.go": "package a\n\nfunc Hello() string { return \"hi\" }\n",
	})
	res, _ := g.Execute(context.Background(), grepCall(map[string]any{
		"pattern": "Hello",
		"output":  "json",
	}))
	if res == nil || !res.Success {
		t.Fatalf("Execute: %+v", res)
	}
	body := grepText(t, res)
	if !strings.HasPrefix(strings.TrimSpace(body), "[") {
		t.Errorf("json output should be a JSON array: %q", body)
	}
	var hits []code.GrepHit
	if err := json.Unmarshal([]byte(body), &hits); err != nil {
		t.Fatalf("json output should parse: %v\n%s", err, body)
	}
	if len(hits) != 1 || hits[0].File != "a.go" || hits[0].Line != 3 {
		t.Errorf("unexpected hits: %+v", hits)
	}
}

// The structured Data exposes the same hits regardless of output mode —
// the TUI renders from these directly, never re-parsing the string.
func TestGrep_DataCarriesTypedHits(t *testing.T) {
	t.Parallel()
	g := grepHarness(t, map[string]string{
		"a.go": "Hello\nHello\n",
	})
	res, _ := g.Execute(context.Background(), grepCall(map[string]any{
		"pattern": "Hello",
	}))
	r, ok := res.Data.(code.GrepResult)
	if !ok {
		t.Fatalf("Data is %T, want code.GrepResult", res.Data)
	}
	if len(r.Hits) != 2 {
		t.Fatalf("want 2 typed hits, got %d", len(r.Hits))
	}
	if r.Hits[0].File != "a.go" || r.Hits[0].Line != 1 {
		t.Errorf("unexpected first hit: %+v", r.Hits[0])
	}
}

// Zero-matches sentinel — model must distinguish "no hits" from
// "tool errored".
func TestGrep_NoMatchesSentinel(t *testing.T) {
	t.Parallel()
	g := grepHarness(t, map[string]string{"a.go": "x"})
	res, _ := g.Execute(context.Background(), grepCall(map[string]any{
		"pattern": "NonexistentSymbol",
	}))
	if !res.Success {
		t.Fatalf("want success even with zero matches, got %+v", res)
	}
	body := grepText(t, res)
	if !strings.Contains(body, "matches: 0") {
		t.Errorf("count header missing: %q", body)
	}
	if !strings.Contains(body, "(no matches)") {
		t.Errorf("no-matches sentinel missing: %q", body)
	}
}

// max_results truncates and the header announces — same as glob.
func TestGrep_MaxResultsTruncatesAndAnnounces(t *testing.T) {
	t.Parallel()
	lines := strings.Repeat("Hello\n", 50)
	g := grepHarness(t, map[string]string{"a.go": lines})
	res, _ := g.Execute(context.Background(), grepCall(map[string]any{
		"pattern":     "Hello",
		"max_results": 5,
	}))
	body := grepText(t, res)
	if !strings.Contains(body, "matches: 5") {
		t.Errorf("count header should report 5: %q", body)
	}
	if !strings.Contains(body, "truncated at cap 5") {
		t.Errorf("output should announce truncation: %q", body)
	}
}

func TestGrep_EmptyPatternRejected(t *testing.T) {
	t.Parallel()
	g := grepHarness(t, nil)
	res, _ := g.Execute(context.Background(), grepCall(map[string]any{
		"pattern": "",
	}))
	if res.Success {
		t.Fatalf("empty pattern: want failure, got %+v", res)
	}
	if !strings.Contains(res.Error, "pattern required") {
		t.Errorf("error should say pattern required: %q", res.Error)
	}
}

// glob filter scopes the search — both extension and path filters
// pass through to ripgrep.
func TestGrep_GlobScopes(t *testing.T) {
	t.Parallel()
	g := grepHarness(t, map[string]string{
		"a.go":     "Hello",
		"a.md":     "Hello",
		"docs.txt": "Hello",
	})
	res, _ := g.Execute(context.Background(), grepCall(map[string]any{
		"pattern": "Hello",
		"glob":    "*.go",
	}))
	body := grepText(t, res)
	if !strings.Contains(body, "a.go") {
		t.Errorf("a.go should match: %q", body)
	}
	if strings.Contains(body, "a.md") || strings.Contains(body, "docs.txt") {
		t.Errorf("non-glob files should be filtered out: %q", body)
	}
}
