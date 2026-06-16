package code

import "testing"

// TestFindHunk_EOFMarkerPinsToTail locks the fix: a hunk carrying an
// *** End of File marker must match the pre-image flush against the end of the
// file, even when the same context appears earlier — otherwise an
// end-of-file edit silently lands on the earlier block.
func TestFindHunk_EOFMarkerPinsToTail(t *testing.T) {
	t.Parallel()
	// "}" appears at index 1 and at the EOF index 3.
	lines := []string{"a {", "}", "b {", "}"}
	h := parsedHunk{endOfFile: true, lines: []parsedHunkLine{{kind: ' ', text: "}"}}}
	m, err := findHunk("f", lines, 0, h)
	if err != nil {
		t.Fatalf("findHunk: %v", err)
	}
	if m.start != 3 || m.end != 4 {
		t.Fatalf("matched [%d,%d), want [3,4) — the EOF '}', not the earlier one", m.start, m.end)
	}
}

// TestFindHunk_NonEOFMatchesFirst confirms the unchanged behaviour: without the
// marker, the same context matches the first occurrence.
func TestFindHunk_NonEOFMatchesFirst(t *testing.T) {
	t.Parallel()
	lines := []string{"a {", "}", "b {", "}"}
	h := parsedHunk{lines: []parsedHunkLine{{kind: ' ', text: "}"}}}
	m, err := findHunk("f", lines, 0, h)
	if err != nil {
		t.Fatalf("findHunk: %v", err)
	}
	if m.start != 1 {
		t.Fatalf("matched start %d, want 1 (first occurrence)", m.start)
	}
}

// TestFindHunk_EOFContextNotAtTailErrors: an EOF-anchored hunk whose context
// isn't at the tail must error rather than silently match an earlier block.
func TestFindHunk_EOFContextNotAtTailErrors(t *testing.T) {
	t.Parallel()
	lines := []string{"x", "y", "z"}
	h := parsedHunk{endOfFile: true, lines: []parsedHunkLine{{kind: ' ', text: "x"}}}
	if _, err := findHunk("f", lines, 0, h); err == nil {
		t.Fatal("expected error: EOF context that isn't at the tail must not silently match earlier")
	}
}
