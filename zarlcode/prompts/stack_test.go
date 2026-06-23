package prompts

import "testing"

func TestNewFragmentMeasuresText(t *testing.T) {
	f := NewFragment(FragmentSkill, "review", "/tmp/review.md", "selected", 2, "first line\nsecond line has words", true)

	if f.Kind != FragmentSkill || f.Name != "review" || f.Source != "/tmp/review.md" || f.Reason != "selected" || f.Order != 2 || !f.Contributes {
		t.Fatalf("fragment metadata mismatch: %#v", f)
	}
	if f.Bytes != len([]byte("first line\nsecond line has words")) {
		t.Fatalf("bytes = %d, want %d", f.Bytes, len([]byte("first line\nsecond line has words")))
	}
	if f.Words != 6 {
		t.Fatalf("words = %d, want 6", f.Words)
	}
	if f.Lines != 2 {
		t.Fatalf("lines = %d, want 2", f.Lines)
	}
}

func TestNewStackTotalsContributingFragmentsWithoutRenderedTotal(t *testing.T) {
	fragments := []Fragment{
		NewFragment(FragmentSystem, "system", "embedded", "active", 0, "system words", true),
		NewFragment(FragmentSkill, "catalogued", "skill.md", "loaded on demand", 1, "skill words ignored", false),
		NewFragment(FragmentRenderedTotal, "rendered", "rendered prompt", "total", 2, "system words plus rendered", true),
	}

	stack := NewStack(fragments)
	if len(stack.Fragments) != len(fragments) {
		t.Fatalf("fragments = %d, want %d", len(stack.Fragments), len(fragments))
	}
	if stack.TotalWords != 2 {
		t.Fatalf("total words = %d, want 2", stack.TotalWords)
	}
	if stack.TotalBytes != len([]byte("system words")) {
		t.Fatalf("total bytes = %d, want %d", stack.TotalBytes, len([]byte("system words")))
	}
	if stack.TotalLines != 1 {
		t.Fatalf("total lines = %d, want 1", stack.TotalLines)
	}
}

func TestNewFragmentEmptyTextHasZeroLines(t *testing.T) {
	f := NewFragment(FragmentSystem, "empty", "test", "empty", 0, "", true)
	if f.Bytes != 0 || f.Words != 0 || f.Lines != 0 {
		t.Fatalf("empty fragment measured as bytes=%d words=%d lines=%d", f.Bytes, f.Words, f.Lines)
	}
}
