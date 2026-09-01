package tui_test

import (
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestGroupToolsGroupedPerIteration(t *testing.T) {
	out := drive(t, teasink.ToolStartedMsg{TaskID: "t", ToolID: "c1", ToolName: "read"}, teasink.ToolCompletedMsg{TaskID: "t", ToolID: "c1", Duration: time.Millisecond}, teasink.ToolStartedMsg{TaskID: "t", ToolID: "c2", ToolName: "grep"}, teasink.ToolCompletedMsg{TaskID: "t", ToolID: "c2", Duration: time.Millisecond})
	if !strings.Contains(out, "tools (2)") {
		t.Fatalf("tool group missing:\n%s", out)
	}
}
func TestThinkingSingleBlockPerTurn(t *testing.T) {
	out := drive(t, teasink.ConversationStartedMsg{TaskID: "t"}, teasink.ThinkingMsg{TaskID: "t", Delta: "before"}, teasink.ThinkingMsg{TaskID: "t", Delta: "after"})
	if strings.Count(out, "[+] thinking") != 1 {
		t.Fatalf("thinking rows != 1:\n%s", out)
	}
}
