package tui_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestPRLineRendersStateAndLink(t *testing.T) {
	got := tui.RenderPRLine(&tui.PRInfo{Number: 42, Title: "Add the thing", State: "OPEN", URL: "https://example.test/pr/42"})
	for _, want := range []string{"#42", "Add the thing", "open", "https://example.test/pr/42"} {
		if !strings.Contains(got, want) {
			t.Errorf("PR line missing %q: %q", want, got)
		}
	}
}
