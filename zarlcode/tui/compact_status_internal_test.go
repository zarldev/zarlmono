package tui

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestAutomaticCompactionUsesStatusBarNotTranscript(t *testing.T) {
	m := New()
	m.timeline.startTurn("turn", 0)
	m.timeline.appendContent("turn", 0, "answer remains conversational")

	m.handleRunnerMsg(teasink.CompactionAppliedMsg{
		TaskID:         "turn",
		MessagesBefore: 10,
		MessagesAfter:  4,
		BytesTrimmed:   1234,
		Engine:         "tiered",
	})

	status := m.statusPane.statusToast()
	for _, want := range []string{"compacted 10→4 msgs", "1.2KB reclaimed", "tiered"} {
		if !strings.Contains(status, want) {
			t.Errorf("status compaction notice missing %q: %q", want, status)
		}
	}
	transcript := strings.Join(m.timeline.renderViewport(80, 20), "\n")
	if strings.Contains(transcript, "compacted") || strings.Contains(transcript, "reclaimed") {
		t.Errorf("compaction telemetry leaked into transcript:\n%s", transcript)
	}
}

func TestManualCompactionCompletionUsesDetailedStatusNotice(t *testing.T) {
	m := New()
	m.applyCompactNowFinished(compactNowFinishedMsg{
		Before: 12, After: 5, BytesTrimmed: 1234, Engine: "summary",
	})
	status := m.statusPane.statusToast()
	for _, want := range []string{"compacted 12→5 msgs", "1.2KB reclaimed", "summary"} {
		if !strings.Contains(status, want) {
			t.Errorf("manual status notice missing %q: %q", want, status)
		}
	}
	if len(m.timeline.items) != 0 {
		t.Fatalf("manual completion should not add transcript items; got %d", len(m.timeline.items))
	}
}
