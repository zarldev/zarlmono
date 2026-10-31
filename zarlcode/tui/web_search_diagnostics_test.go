package tui_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/search"
)

func TestWebSearchDegradedPresentation(t *testing.T) {
	r := search.Result{
		Query: "Go contexts", Output: tools.OutputJSON,
		Hits:     []search.Hit{{Title: "Context guide", URL: "https://example.com/docs"}},
		Failures: []search.SourceFailure{{Source: "duckduckgo", Reason: "challenge"}},
	}
	serialized, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		text string
		data any
	}{
		{name: "typed", data: r},
		{name: "compact JSON", text: r.String()},
		{name: "serialized Result", text: string(serialized)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := ansi.Strip(strings.Join(tui.RenderContent(80, tui.Content{Kind: tui.ContentToolResult, ToolName: "web_search", Text: tc.text, Data: tc.data}), "\n"))
			for _, want := range []string{"1 results", "Context guide", "https://example.com/docs", "search degraded:", "duckduckgo: challenge"} {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q in %s", want, out)
				}
			}
		})
	}
}
