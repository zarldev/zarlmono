package prompts_test

import (
	"testing"

	prompts "github.com/zarldev/zarlmono/zarlcode/prompts"
)

func TestNewFragmentMeasuresText(t *testing.T) {
	f := prompts.NewFragment(prompts.FragmentSkill, "review", "/tmp/review.md", "selected", 2, "first line\nsecond line has words", true)

	if f.Kind != prompts.FragmentSkill || f.Name != "review" || f.Source != "/tmp/review.md" || f.Reason != "selected" || f.Order != 2 || !f.Contributes {
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
	fragments := []prompts.Fragment{
		prompts.NewFragment(prompts.FragmentSystem, "system", "embedded", "active", 0, "system words", true),
		prompts.NewFragment(prompts.FragmentSkill, "catalogued", "skill.md", "loaded on demand", 1, "skill words ignored", false),
		prompts.NewFragment(prompts.FragmentRenderedTotal, "rendered", "rendered prompt", "total", 2, "system words plus rendered", true),
	}

	stack := prompts.NewStack(fragments)
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
	if stack.RenderedWords != 4 {
		t.Fatalf("rendered words = %d, want 4", stack.RenderedWords)
	}
	if stack.RenderedBytes != len([]byte("system words plus rendered")) {
		t.Fatalf("rendered bytes = %d, want %d", stack.RenderedBytes, len([]byte("system words plus rendered")))
	}
	if stack.RenderedLines != 1 {
		t.Fatalf("rendered lines = %d, want 1", stack.RenderedLines)
	}
}

func TestNewFragmentEmptyTextHasZeroLines(t *testing.T) {
	f := prompts.NewFragment(prompts.FragmentSystem, "empty", "test", "empty", 0, "", true)
	if f.Bytes != 0 || f.Words != 0 || f.Lines != 0 {
		t.Fatalf("empty fragment measured as bytes=%d words=%d lines=%d", f.Bytes, f.Words, f.Lines)
	}
}
