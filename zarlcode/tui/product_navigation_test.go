package tui_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestProductNavigationTranscriptPreservesSourceMessages(t *testing.T) {
	ui := tui.New()
	ui.AddTranscriptUser("first request")
	ui.AddTranscriptUser("second request with needle")
	got := strings.Join(ui.RenderTranscript(80, 20), "\n")
	for _, want := range []string{"first request", "second request with needle"} {
		if !strings.Contains(got, want) {
			t.Fatalf("transcript missing %q:\n%s", want, got)
		}
	}
}
