package tui

import (
	"strings"
	"testing"
)

func TestManualCompactionEventAddsCollapsedTranscriptItem(t *testing.T) {
	m := New()
	m.timeline.attachCompaction("manual-compact", compactionNotice(12, 5, 1234, "summary"))

	if len(m.timeline.items) != 1 {
		t.Fatalf("timeline items = %d, want one manual compaction item", len(m.timeline.items))
	}
	item, ok := m.timeline.items[0].(*compactionItem)
	if !ok {
		t.Fatalf("timeline item = %T, want *compactionItem", m.timeline.items[0])
	}
	collapsed := strings.Join(item.render(80), "\n")
	for _, want := range []string{"[+]", "compacted", "12→5 msgs", "summary"} {
		if !strings.Contains(collapsed, want) {
			t.Errorf("collapsed compaction item missing %q: %q", want, collapsed)
		}
	}
	if strings.Contains(collapsed, "manual conversation compaction") {
		t.Errorf("collapsed compaction item should hide details: %q", collapsed)
	}

	item.toggle()
	expanded := strings.Join(item.render(80), "\n")
	for _, want := range []string{"[-]", "manual conversation compaction", "1.2KB reclaimed"} {
		if !strings.Contains(expanded, want) {
			t.Errorf("expanded compaction item missing %q: %q", want, expanded)
		}
	}
}

func TestApplyCompactNowFinishedDoesNotAddDuplicateTranscriptItem(t *testing.T) {
	m := New()
	m.applyCompactNowFinished(compactNowFinishedMsg{Before: 12, After: 5, BytesTrimmed: 1234, Engine: "summary"})
	if len(m.timeline.items) != 0 {
		t.Fatalf("completion should not add a duplicate transcript item; got %d", len(m.timeline.items))
	}
}

func TestAutomaticCompactionAttachesToExistingAssistantTurn(t *testing.T) {
	tl := newTimeline()
	tl.startTurn("turn", 0)
	tl.attachCompaction("turn", "↯ compacted 10→4 msgs · tiered")

	if len(tl.items) != 2 {
		t.Fatalf("timeline items = %d, want assistant response and skills item only", len(tl.items))
	}
	turn := tl.turns["turn"]
	if turn == nil || turn.resp == nil {
		t.Fatal("existing assistant turn was lost")
	}
	if !strings.Contains(turn.resp.compactionNotice, "compacted 10→4 msgs") {
		t.Errorf("automatic compaction notice = %q", turn.resp.compactionNotice)
	}
}
