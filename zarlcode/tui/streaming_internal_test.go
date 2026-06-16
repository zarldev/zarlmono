package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestFindSafeMarkdownBoundary(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		wantSafe bool
	}{
		{"two paragraphs", "para one\n\npara two", true},
		{"blank inside open fence", "```go\nline1\n\nline2\n", false},
		{"closed fence then para", "```go\nx := 1\n```\n\nafter the code", true},
		{"list closed by para", "- a\n- b\n\nmore items", true},
		{"open list at end", "- a\n- b\n\n", false},
		{"boundary before list", "intro line\n\n- a\n- b", true},
		{"no blank line", "single line only", false},
		{"setext underline ahead", "Title\n\n===", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := findSafeMarkdownBoundary(c.in)
			switch {
			case c.wantSafe && b < 0:
				t.Errorf("expected a safe boundary, got -1")
			case !c.wantSafe && b >= 0:
				t.Errorf("expected -1, got boundary %d (prefix %q)", b, c.in[:b])
			case b > 0 && !strings.HasPrefix(c.in, c.in[:b]):
				t.Errorf("boundary not a prefix offset")
			}
		})
	}
}

func TestStreamingMarkdown_PreservesContent(t *testing.T) {
	const doc = "# Title\n\nFirst paragraph here.\n\nSecond paragraph with detail.\n\nThird and final paragraph."
	var s streamingMarkdown
	var out string
	for i := 1; i <= len(doc); i++ {
		out = s.render(doc[:i], 60)
	}
	plain := ansi.Strip(out)
	for _, want := range []string{"Title", "First paragraph", "Second paragraph", "final paragraph"} {
		if !strings.Contains(plain, want) {
			t.Errorf("streamed render lost %q:\n%s", want, plain)
		}
	}
}

func TestStreamingMarkdown_AdvancesStablePrefix(t *testing.T) {
	var s streamingMarkdown
	doc := "Intro paragraph.\n\nMiddle paragraph.\n\nStill streaming the la"
	s.render(doc, 60)
	if s.stablePrefix == "" {
		t.Error("expected a non-empty stable prefix once safe boundaries exist")
	}
	if !strings.HasPrefix(doc, s.stablePrefix) {
		t.Errorf("stablePrefix must be a literal prefix of content; got %q", s.stablePrefix)
	}
}

func TestStreamingMarkdown_Deterministic(t *testing.T) {
	const doc = "# Heading\n\na paragraph"
	var a, b streamingMarkdown
	if a.render(doc, 50) != b.render(doc, 50) {
		t.Error("same content+width must render identically regardless of instance")
	}
}
