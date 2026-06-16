package tui_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestTimeline_ThinkingSplitFromAnswer(t *testing.T) {
	out := drive(t,
		teasink.ConversationStartedMsg{TaskID: "t1", Prompt: "q"},
		teasink.ThinkingMsg{TaskID: "t1", Delta: "my private reasoning"},
		teasink.ContentMsg{TaskID: "t1", Delta: "the visible answer"},
	)
	if !strings.Contains(out, "the visible answer") {
		t.Errorf("answer missing:\n%s", out)
	}
	if !strings.Contains(out, "thinking") {
		t.Errorf("collapsed thinking header missing:\n%s", out)
	}
	if strings.Contains(out, "my private reasoning") {
		t.Errorf("thinking leaked into transcript (should be collapsed):\n%s", out)
	}
}
