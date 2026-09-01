package tui_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestBrowseWindowPreservesVisibleTranscriptTail(t *testing.T) {
	ui := tui.New()
	ui.AddTranscriptUser("oldest message")
	ui.AddTranscriptUser("newest message")
	got := strings.Join(ui.RenderTranscript(40, 6), "\n")
	if !strings.Contains(got, "newest message") {
		t.Fatalf("transcript tail missing newest message:\n%s", got)
	}
}

func BenchmarkBrowseWindowRender(b *testing.B) {
	ui := tui.New()
	for range 1000 {
		ui.AddTranscriptUser("user message with enough text to wrap across the transcript")
	}
	b.ResetTimer()
	for range b.N {
		ui.RenderTranscript(80, 30)
	}
}
