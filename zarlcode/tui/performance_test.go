package tui_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func BenchmarkTimelineRenderTail(b *testing.B) {
	ui := tui.New()
	for range 10000 {
		ui.AddTranscriptUser("message with enough text to wrap across the viewport")
	}
	b.ResetTimer()
	for range b.N {
		ui.RenderTranscript(80, 30)
	}
}
