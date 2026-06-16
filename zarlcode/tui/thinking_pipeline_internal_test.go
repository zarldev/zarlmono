package tui

import (
	"strings"
	"testing"
)

// A raw reasoning delta (from the runner's thinking event) must route to the
// turn's thinking item — without the <think>-tag re-parse appendContent
// applies. This is the path that makes Anthropic extended thinking (which
// only ever sets chunk.Thinking, never inline tags) reach the transcript.
func TestTimelineAppendThinkingRoutesToThinkItem(t *testing.T) {
	tl := newTimeline()
	tl.startTurn("t1", 0)
	tl.appendThinking("t1", 0, "weighing the trade-offs here")

	ot := tl.turns["t1"]
	if ot == nil || ot.think == nil {
		t.Fatal("appendThinking should create a thinking item on the turn")
	}
	if !strings.Contains(ot.think.text, "weighing the trade-offs here") {
		t.Errorf("reasoning should be stored on the thinking item, got %q", ot.think.text)
	}
	// The visible response stays empty (reasoning isn't answer text) and shows
	// the thinking placeholder.
	if ot.resp.content != "" {
		t.Errorf("reasoning must not leak into the response body, got %q", ot.resp.content)
	}
}

// appendThinking takes raw text — it must NOT strip a literal "<think>"
// that appears in reasoning (the tag re-parse lives only on the content path).
func TestTimelineAppendThinkingDoesNotReparseTags(t *testing.T) {
	tl := newTimeline()
	tl.startTurn("t2", 0)
	tl.appendThinking("t2", 0, "consider the <think> sentinel literally")

	if got := tl.turns["t2"].think.text; !strings.Contains(got, "<think>") {
		t.Errorf("raw reasoning must pass through unparsed (keep <think>), got %q", got)
	}
}
