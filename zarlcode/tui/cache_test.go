package tui_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestTranscriptRenderRetainsVisibleHistory(t *testing.T) {
	ui := tui.New()
	ui.AddTranscriptUser("oldest visible message")
	for range 20 {
		ui.AddTranscriptUser("filler message")
	}
	ui.AddTranscriptUser("newest visible message")
	got := strings.Join(ui.RenderTranscript(80, 5), "\n")
	if !strings.Contains(got, "newest visible message") {
		t.Fatalf("visible tail lost:\n%s", got)
	}
}
