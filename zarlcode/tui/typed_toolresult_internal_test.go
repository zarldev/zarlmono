package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// A grep result renders from its typed Hits — grouped by file, no colon
// heuristic — and ignores the formatted text string entirely.
func TestRenderTypedToolResult_GrepFromFields(t *testing.T) {
	b := contentBlock{
		kind:     contentToolResult,
		toolName: "grep",
		text:     "SHOULD-NOT-APPEAR",
		data: code.GrepResult{Hits: []code.GrepHit{
			{File: "a.go", Line: 3, Text: "func Hello()"},
			{File: "a.go", Line: 7, Text: "Hello again"},
			{File: "b.go", Line: 1, Text: "Hello"},
		}},
	}
	out := ansi.Strip(strings.Join(renderTypedToolResult(80, b), "\n"))

	if strings.Contains(out, "SHOULD-NOT-APPEAR") {
		t.Errorf("typed render must come from fields, not b.text:\n%s", out)
	}
	// File header appears once even though a.go has two hits (file-grouped).
	if n := strings.Count(out, "a.go"); n != 1 {
		t.Errorf("a.go header should appear once, got %d:\n%s", n, out)
	}
	for _, want := range []string{"a.go", "3: func Hello()", "7: Hello again", "b.go", "1: Hello"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// A long match line wraps under the line-number gutter rather than being
// clipped — same width discipline as the rest of the transcript.
func TestRenderTypedToolResult_GrepWraps(t *testing.T) {
	b := contentBlock{
		kind:     contentToolResult,
		toolName: "grep",
		data:     code.GrepResult{Hits: []code.GrepHit{{File: "a.go", Line: 1, Text: strings.Repeat("x", 200)}}},
	}
	for _, ln := range renderTypedToolResult(40, b) {
		if w := ansi.StringWidth(ln); w > 40 {
			t.Errorf("row width %d exceeds 40: %q", w, ln)
		}
	}
}

// Non-structured data returns nil so renderToolResultContent falls back to the
// text path (bash, read, restored sessions).
func TestRenderTypedToolResult_NilFallsBack(t *testing.T) {
	b := contentBlock{kind: contentToolResult, toolName: "bash", text: "$ ls\nfile.go"}
	if got := renderTypedToolResult(80, b); got != nil {
		t.Errorf("unstructured result should return nil for fallback, got %v", got)
	}
}
