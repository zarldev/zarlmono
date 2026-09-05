package tui_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestAttachmentMetadataSurvivesCanonicalReplayProjection(t *testing.T) {
	ui := tui.New()
	ui.ReplayTranscriptEvents(transcript.UserSubmitted{
		Text:        "inspect",
		Attachments: []transcript.Attachment{{Name: "notes.txt", MIMEType: "text/plain", Size: 42}},
	})
	step(t, ui, window(100, 20))
	view := ui.View().Content
	if !strings.Contains(view, "notes.txt") || !strings.Contains(view, "text/plain") {
		t.Fatalf("attachment projection missing from view: %q", view)
	}
}
