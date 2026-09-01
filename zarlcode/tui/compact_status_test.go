package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestAutomaticCompactionUsesStatusBarNotTranscript(t *testing.T) {
	var model tea.Model = tui.New()
	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	model, _ = model.Update(teasink.ConversationStartedMsg{TaskID: "turn", Prompt: "question"})
	model, _ = model.Update(teasink.ContentMsg{TaskID: "turn", Delta: "answer remains conversational"})
	model, _ = model.Update(teasink.CompactionAppliedMsg{
		TaskID: "turn", MessagesBefore: 10, MessagesAfter: 4, BytesTrimmed: 1234, Engine: "tiered",
	})

	out := ansi.Strip(model.View().Content)
	for _, want := range []string{"compacted 10→4 msgs", "1.2KB reclaimed", "tiered", "answer remains conversational"} {
		if !strings.Contains(out, want) {
			t.Errorf("UI missing %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "compacted 10→4 msgs") != 1 {
		t.Errorf("compaction notice should appear only in status UI:\n%s", out)
	}
}
