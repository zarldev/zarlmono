package code_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// lsHarness builds a workspace containing the given files and
// directories and returns a ready-to-call LsTool.
//
// Paths ending in "/" are created as empty directories; anything else
// is a file with the given body.
func lsHarness(t *testing.T, entries map[string]string) *code.LsTool {
	t.Helper()
	root := t.TempDir()
	for rel, body := range entries {
		abs := filepath.Join(root, rel)
		if strings.HasSuffix(rel, "/") {
			if err := os.MkdirAll(abs, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", abs, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir parent %s: %v", abs, err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", abs, err)
		}
	}
	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	return code.NewLsTool(ws)
}

func lsCall(args map[string]any) tools.ToolCall {
	return tools.ToolCall{
		ID:        "c",
		ToolName:  code.ToolNameLs,
		Arguments: tools.ToolParameters(args),
	}
}

// lsText renders the model-facing string for a result — the same text
// the runner flattens LsResult to via fmt.Stringer.
func lsText(t *testing.T, res *tools.ToolResult) string {
	t.Helper()
	r, ok := res.Data.(code.LsResult)
	if !ok {
		t.Fatalf("Data is %T, want code.LsResult", res.Data)
	}
	return r.String()
}

// Default output is labelled, not JSON: header carries the count + path,
// rows are indented, dirs get a trailing slash + (dir) marker. Canonical
// reference for the labelled-ls shape — if this starts asserting JSON the
// convention has slipped.
func TestLs_DefaultOutputShape(t *testing.T) {
	t.Parallel()
	g := lsHarness(t, map[string]string{
		"main.go":  "package main",
		"cmd/":     "",
		"notes.md": "x",
	})
	res, _ := g.Execute(context.Background(), lsCall(map[string]any{}))
	if res == nil || !res.Success {
		t.Fatalf("Execute: %+v", res)
	}
	body := lsText(t, res)
	for _, forbid := range []string{"{", "}", "[", "]", "\":\""} {
		if strings.Contains(body, forbid) {
			t.Errorf("output contains JSON-shaped %q (must be labelled plaintext): %q", forbid, body)
		}
	}
	if !strings.HasPrefix(body, "entries: ") {
		t.Errorf("output missing labelled count header: %q", body)
	}
	if !strings.Contains(body, "cmd/") {
		t.Errorf("dir should have trailing slash: %q", body)
	}
	if !strings.Contains(body, "(dir)") {
		t.Errorf("dir should have (dir) marker: %q", body)
	}
	if !strings.Contains(body, "main.go") {
		t.Errorf("file missing: %q", body)
	}
}

// output="json" switches the model-facing rendering to a JSON array of
// {name, type, size} — same structured entries, different String().
func TestLs_JSONOutput(t *testing.T) {
	t.Parallel()
	g := lsHarness(t, map[string]string{
		"main.go": "package main",
		"cmd/":    "",
	})
	res, _ := g.Execute(context.Background(), lsCall(map[string]any{"output": "json"}))
	if res == nil || !res.Success {
		t.Fatalf("Execute: %+v", res)
	}
	body := lsText(t, res)
	if !strings.HasPrefix(strings.TrimSpace(body), "[") {
		t.Errorf("json output should be a JSON array: %q", body)
	}
	var entries []struct {
		Name string `json:"name"`
		Type string `json:"type"`
		Size int64  `json:"size"`
	}
	if err := json.Unmarshal([]byte(body), &entries); err != nil {
		t.Fatalf("json output should parse: %v\n%s", err, body)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d: %+v", len(entries), entries)
	}
	// Entries are sorted by name, so cmd (dir) precedes main.go.
	if entries[0].Name != "cmd" || entries[0].Type != "dir" {
		t.Errorf("first entry should be cmd dir: %+v", entries[0])
	}
}

// The structured Data exposes the typed entries regardless of output
// mode — a consumer renders from these directly, never re-parsing.
func TestLs_DataCarriesTypedEntries(t *testing.T) {
	t.Parallel()
	g := lsHarness(t, map[string]string{"a.go": "x", "b.go": "x"})
	res, _ := g.Execute(context.Background(), lsCall(map[string]any{}))
	r, ok := res.Data.(code.LsResult)
	if !ok {
		t.Fatalf("Data is %T, want code.LsResult", res.Data)
	}
	if len(r.Entries) != 2 {
		t.Fatalf("want 2 typed entries, got %d", len(r.Entries))
	}
	if r.Entries[0].Name != "a.go" {
		t.Errorf("entries should be sorted by name, got first %q", r.Entries[0].Name)
	}
}

// Dotfiles are excluded by default. show_hidden=true reveals them.
func TestLs_HidesDotfilesByDefault(t *testing.T) {
	t.Parallel()
	g := lsHarness(t, map[string]string{
		"main.go":    "x",
		".gitignore": "x",
	})
	res, _ := g.Execute(context.Background(), lsCall(map[string]any{}))
	body := lsText(t, res)
	if strings.Contains(body, ".gitignore") {
		t.Errorf("dotfile leaked: %q", body)
	}
	if !strings.Contains(body, "main.go") {
		t.Errorf("main.go missing: %q", body)
	}
}

func TestLs_ShowHiddenIncludesDotfiles(t *testing.T) {
	t.Parallel()
	g := lsHarness(t, map[string]string{
		"main.go":    "x",
		".gitignore": "x",
	})
	res, _ := g.Execute(context.Background(), lsCall(map[string]any{
		"show_hidden": true,
	}))
	body := lsText(t, res)
	if !strings.Contains(body, ".gitignore") {
		t.Errorf("dotfile missing with show_hidden=true: %q", body)
	}
	if !strings.Contains(body, "(showing hidden)") {
		t.Errorf("header should advertise show_hidden mode: %q", body)
	}
}

// path arg scopes the listing.
func TestLs_PathScopes(t *testing.T) {
	t.Parallel()
	g := lsHarness(t, map[string]string{
		"cmd/main.go":    "x",
		"cmd/other.go":   "x",
		"pkg/foo/foo.go": "x",
	})
	res, _ := g.Execute(context.Background(), lsCall(map[string]any{
		"path": "cmd",
	}))
	body := lsText(t, res)
	if !strings.Contains(body, "main.go") || !strings.Contains(body, "other.go") {
		t.Errorf("cmd/ contents missing: %q", body)
	}
	if strings.Contains(body, "foo.go") {
		t.Errorf("pkg/foo leaked into cmd/ listing: %q", body)
	}
	if !strings.Contains(body, "path: cmd") {
		t.Errorf("header should report path: %q", body)
	}
}

// Empty directory → header + (empty) sentinel. The model needs to
// distinguish "empty" from "errored".
func TestLs_EmptyDirSentinel(t *testing.T) {
	t.Parallel()
	g := lsHarness(t, map[string]string{
		"empty/": "",
	})
	res, _ := g.Execute(context.Background(), lsCall(map[string]any{
		"path": "empty",
	}))
	if !res.Success {
		t.Fatalf("want success on empty dir, got %+v", res)
	}
	body := lsText(t, res)
	if !strings.Contains(body, "entries: 0") {
		t.Errorf("count header missing: %q", body)
	}
	if !strings.Contains(body, "(empty)") {
		t.Errorf("empty sentinel missing: %q", body)
	}
}

// Path outside the workspace returns a permission error. Workspace
// boundary is the security guarantee.
func TestLs_PathOutsideWorkspaceRejected(t *testing.T) {
	t.Parallel()
	g := lsHarness(t, nil)
	res, _ := g.Execute(context.Background(), lsCall(map[string]any{
		"path": "../escape",
	}))
	if res.Success {
		t.Fatalf("escape attempt should fail, got %+v", res)
	}
}
