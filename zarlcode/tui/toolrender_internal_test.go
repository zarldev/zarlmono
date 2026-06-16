package tui

import (
	"strings"
	"testing"
)

func TestToolArgHint(t *testing.T) {
	cases := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{"bash", map[string]any{"command": "go test ./..."}, "$ go test ./..."},
		{"read_file", map[string]any{"path": "main.go"}, "main.go"},
		{"grep", map[string]any{"pattern": "func main"}, "func main"},
		{"load_skill", map[string]any{"name": "go-testing"}, "go-testing"},
		{"spawn_agent", map[string]any{"prompt": "fix the bug\nmore detail"}, "fix the bug"},
		{"spawn_agent", map[string]any{"agent": "reviewer", "prompt": "review the patch"}, "reviewer: review the patch"},
		{"unknown_tool", map[string]any{"x": "y"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := toolArgHint(c.name, c.params); got != c.want {
				t.Errorf("toolArgHint(%q) = %q, want %q", c.name, got, c.want)
			}
		})
	}
}

func TestToolArgHint_TruncatesLong(t *testing.T) {
	got := toolArgHint("bash", map[string]any{"command": strings.Repeat("x", 100)})
	if r := []rune(got); len(r) > 80 || r[len(r)-1] != '…' {
		t.Errorf("expected truncation to <=80 runes ending in ellipsis, got %d: %q", len(r), got)
	}
}
