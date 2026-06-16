package tui

import (
	"strings"
	"testing"
)

// TestFindSafeMarkdownBoundary_Characterization pins the exact offset
// returned by findSafeMarkdownBoundary across a broad markdown corpus.
// The parser rejects on any doubt, and the rejections are load-bearing
// for safe streaming — but hazards are scoped to their construct's
// lifetime, so a boundary becomes available again once a list provably
// closes or a generic HTML block ends at a blank line. Document-scoped
// constructs (link reference definitions, raw-HTML blocks) stay
// rejected for the rest of the document.
func TestFindSafeMarkdownBoundary_Characterization(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"two_paragraphs", "para one\n\npara two", 10},
		{"three_paragraphs", "a\n\nb\n\nc", 6},
		{"open_fence", "```go\nline1\n\nline2\n", -1},
		{"closed_fence_then_para", "```go\nx := 1\n```\n\nafter the code", 18},
		{"list_closed_by_paragraph", "- a\n- b\n\nmore items", 9},
		{"boundary_before_list", "intro line\n\n- a\n- b", 12},
		{"no_blank_line", "single line only", -1},
		{"setext_underline_ahead", "Title\n\n===", -1},
		{"setext_dash_ahead", "Title\n\n---", -1},
		{"blockquote_open", "intro\n\n> quoted line", 7},
		{"table_open", "intro\n\n| a | b |", 7},
		{"indented_code_open", "intro\n\n    code block", 7},
		{"link_ref_def", "intro\n\n[id]: http://example.com", 7},
		{"link_then_more", "[id]: http://x.com\n\nbody", -1},
		{"html_block_open", "intro\n\n<div>", 7},
		{"html_comment_block", "intro\n\n<!-- comment", 7},
		{"para_then_closed_fence", "intro\n\n```\ncode\n```\n\ntail text", 21},
		{"multiple_safe_last_wins", "one\n\ntwo\n\nthree\n\nfour text", 17},
		{"fence_with_blank_inside", "```\na\n\nb\n```\n\ndone", 14},
		{"nested_list_closed_by_paragraph", "- a\n  - b\n\nafter", 11},
		{"trailing_blank_lines", "para\n\n\n\nmore", 8},
		{"ordered_list_open", "intro\n\n1. first", 7},
		{"empty", "", -1},
		{"only_blank", "\n\n", 2},
		{"heading_then_para", "# H\n\nbody text here", 5},
		{"two_para_trailing_partial", "Intro paragraph.\n\nMiddle paragraph.\n\nStill streaming the la", 37},

		// Scoped hazards: caching resumes after a construct closes.
		{"list_closed_then_later_boundary", "- a\n- b\n\nplain para\n\nnext", 21},
		{"list_same_marker_stays_open", "- a\n\n- b", -1},
		{"list_open_at_end_no_end_boundary", "- a\n- b\n\n", -1},
		{"ordered_list_closed_by_paragraph", "1. a\n\npara", 6},
		{"partial_dash_is_not_a_terminator", "- a\n\n-", -1},
		{"partial_ordinal_is_not_a_terminator", "- a\n\n12", -1},
		{"partial_ordinal_dot_is_not_a_terminator", "- a\n\n12.", -1},
		{"indented_line_keeps_list_open", "- a\n\n  cont", -1},
		{"html_block_closed_by_blank", "intro\n\n<div>\nx\n\nafter", 16},
		// Sticky constructs: the boundary before the opener stays the
		// latest safe one; nothing after the opener ever qualifies.
		{"script_block_blocks_later_boundaries", "intro\n\n<script>\nx\n\nafter", 7},
		{"comment_block_blocks_later_boundaries", "intro\n\n<!-- note\n\nafter", 7},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := findSafeMarkdownBoundary(c.in)
			if got != c.want {
				t.Fatalf("findSafeMarkdownBoundary(%q) = %d, want %d", c.in, got, c.want)
			}
			if got > 0 && (got > len(c.in) || !strings.HasPrefix(c.in, c.in[:got])) {
				t.Fatalf("offset %d is not a valid prefix boundary of %q", got, c.in)
			}
		})
	}
}

func BenchmarkFindSafeMarkdownBoundary(b *testing.B) {
	// A long document dominated by blank lines is the pathological
	// input for the old backward scan: every blank line is a boundary
	// candidate, and each candidate re-walked the whole prefix.
	var sb strings.Builder
	for i := range 1500 {
		sb.WriteString("paragraph line number ")
		sb.WriteString(strings.Repeat("x", i%40))
		sb.WriteString("\n\n")
	}
	doc := sb.String()
	b.ResetTimer()
	for range b.N {
		_ = findSafeMarkdownBoundary(doc)
	}
}
