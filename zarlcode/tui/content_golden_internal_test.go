package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

// assertGolden writes the rendered lines to testdata/golden/<name>.ansi the
// first time the test runs, then compares on subsequent runs. Update golden
// files by deleting them or running with -update-golden.
//
// Lines are joined with newline and a single trailing newline so the files
// are human-readable diffs.
func assertGolden(t *testing.T, lines []string) {
	t.Helper()

	path := filepath.Join("testdata", "golden", t.Name()+".ansi")
	got := strings.Join(lines, "\n") + "\n"

	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir golden dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v\nrun with UPDATE_GOLDEN=1 to write initial snapshots", path, err)
	}
	if string(want) != got {
		t.Fatalf("golden mismatch %s:\n--- want\n+++ got\n%s", path, unifiedDiff(string(want), got))
	}
}

func unifiedDiff(a, b string) string {
	alines := strings.Split(a, "\n")
	blines := strings.Split(b, "\n")
	// Simple line-by-line diff for test output.
	var out strings.Builder
	maxN := len(alines)
	if len(blines) > maxN {
		maxN = len(blines)
	}
	for i := range maxN {
		var aLine, bLine string
		if i < len(alines) {
			aLine = alines[i]
		}
		if i < len(blines) {
			bLine = blines[i]
		}
		if aLine != bLine {
			out.WriteString("-")
			out.WriteString(aLine)
			out.WriteString("\n+")
			out.WriteString(bLine)
			out.WriteString("\n")
		}
	}
	return out.String()
}

// --- Golden tests for key render paths ---

func TestGolden_AssistantMarkdown(t *testing.T) {
	UseTheme(theme.DarkDefault())
	defer UseTheme(theme.Theme{})

	out := renderContentBlock(80, contentBlock{
		kind: contentMarkdown,
		text: "# Heading\n\nA **bold** and *italic* paragraph.\n\n- list item\n- another\n\n```go\nfunc main() {}\n```",
	})
	assertGolden(t, out)
}

func TestGolden_ThinkingMutedMarkdown(t *testing.T) {
	UseTheme(theme.DarkDefault())
	defer UseTheme(theme.Theme{})

	out := renderContentBlock(76, contentBlock{
		kind:      contentMarkdown,
		text:      "## Reasoning\n\nI considered **several** options.\n\n- trade-off one\n- trade-off two",
		stripANSI: true,
		tone:      toneMuted,
	})
	assertGolden(t, out)
}

func TestGolden_CodeBlockWithLineNumbers(t *testing.T) {
	UseTheme(theme.DarkDefault())
	defer UseTheme(theme.Theme{})

	out := renderContentBlock(80, contentBlock{
		kind:        contentCode,
		text:        "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}",
		syntax:      "go",
		lineNumbers: true,
	})
	assertGolden(t, out)
}

func TestGolden_DiffBody(t *testing.T) {
	UseTheme(theme.DarkDefault())
	defer UseTheme(theme.Theme{})

	out := renderContentBlock(80, contentBlock{
		kind:       contentDiff,
		text:       "@@ pkg/foo.go @@\n package foo\n+func New() {}\n-func Old() {}\n context unchanged",
		maxLines:   10,
		bodyPrefix: "  ",
	})
	assertGolden(t, out)
}

func TestGolden_GrepSearchResults(t *testing.T) {
	UseTheme(theme.DarkDefault())
	defer UseTheme(theme.Theme{})

	out := renderContentBlock(80, contentBlock{
		kind:     contentToolResult,
		text:     "pkg/foo/bar.go:42:\tfmt.Println(\"hello\")\npkg/foo/baz.go:15:\treturn nil",
		toolName: "grep",
	})
	assertGolden(t, out)
}

func TestGolden_BashTerminalOutput(t *testing.T) {
	UseTheme(theme.DarkDefault())
	defer UseTheme(theme.Theme{})

	out := renderContentBlock(80, contentBlock{
		kind:     contentToolResult,
		text:     "total 24\ndrwxr-xr-x  5 user  staff  160 May 31 12:00 .\ndrwxr-xr-x  3 user  staff   96 May 31 11:00 ..\n-rw-r--r--  1 user  staff  123 May 31 10:00 README.md",
		toolName: "bash",
	})
	assertGolden(t, out)
}

func TestGolden_JSONPrettyPrinted(t *testing.T) {
	UseTheme(theme.DarkDefault())
	defer UseTheme(theme.Theme{})

	out := renderContentBlock(80, contentBlock{
		kind:     contentToolResult,
		text:     `{"name":"zarlcode","version":"1.0","deps":["go","glamour"]}`,
		toolName: "mcp_call",
	})
	assertGolden(t, out)
}
