package tui_test

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/zarldev/zarlmono/zarlcode/tui"
	program "github.com/zarldev/zarlmono/zkit/agent/tools/program"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestRenderTypedToolResultGrepFromFields(t *testing.T) {
	r := code.GrepResult{Hits: []code.GrepHit{{File: "a.go", Line: 3, Text: "func Hello()"}, {File: "a.go", Line: 7, Text: "Hello again"}, {File: "b.go", Line: 1, Text: "Hello"}}}
	out := ansi.Strip(strings.Join(tui.RenderTypedToolResult(80, "grep", "SHOULD-NOT-APPEAR", r), "\n"))
	if strings.Contains(out, "SHOULD-NOT-APPEAR") || strings.Count(out, "a.go") != 1 {
		t.Fatalf("bad output:\n%s", out)
	}
	for _, w := range []string{"3: func Hello()", "7: Hello again", "b.go", "1: Hello"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q", w)
		}
	}
}
func TestRenderTypedToolResultGrepWraps(t *testing.T) {
	r := code.GrepResult{Hits: []code.GrepHit{{File: "a.go", Line: 1, Text: strings.Repeat("x", 200)}}}
	for _, ln := range tui.RenderTypedToolResult(40, "grep", "", r) {
		if ansi.StringWidth(ln) > 40 {
			t.Errorf("wide %q", ln)
		}
	}
}
func TestRenderTypedToolResultNilFallsBack(t *testing.T) {
	if got := tui.RenderTypedToolResult(80, "bash", "$ ls", nil); got != nil {
		t.Errorf("got %v", got)
	}
}
func TestRenderTypedToolResultProgram(t *testing.T) {
	r := program.Result{Output: map[string]any{"answer": "yes"}, Stats: program.Stats{ToolCalls: 2, ParallelBatches: 1}}
	out := ansi.Strip(strings.Join(tui.RenderTypedToolResult(80, "program", "ignored", r), "\n"))
	for _, w := range []string{"\"answer\": \"yes\"", "program: 2 calls, 1 parallel"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q:\n%s", w, out)
		}
	}
}
func TestRenderTypedToolResultProgramCompact(t *testing.T) {
	r := program.Result{Output: []any{map[string]any{"ok": true, "data": map[string]any{"verbose": strings.Repeat("x", 200)}}, map[string]any{"ok": false, "error": "bad thing happened\nwith details"}}, Stats: program.Stats{ToolCalls: 2}}
	out := ansi.Strip(strings.Join(tui.RenderTypedToolResult(70, "program", "", r), "\n"))
	for _, w := range []string{"✓ result 1", "✗ result 2: bad thing happened", "program: 2 calls"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q:\n%s", w, out)
		}
	}
}
func TestRenderTypedToolResultProgramSummaries(t *testing.T) {
	r := program.Result{Output: []any{map[string]any{"ok": true, "data": map[string]any{"Payload": map[string]any{"files": []any{"a", "b"}}, "Output": "labeled"}}, map[string]any{"ok": true, "data": map[string]any{"Hits": []any{"h1", "h2", "h3"}}}}}
	out := ansi.Strip(strings.Join(tui.RenderTypedToolResult(80, "program", "", r), "\n"))
	for _, w := range []string{"file_map: 2 files", "grep: 3 hits"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q:\n%s", w, out)
		}
	}
}
