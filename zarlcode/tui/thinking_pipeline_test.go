package tui_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestTimelineAppendThinkingRoutesToThinkItem(t *testing.T) {
	out := drive(t,
		teasink.ConversationStartedMsg{TaskID: "t1", Prompt: "question"},
		teasink.ThinkingMsg{TaskID: "t1", Delta: "weighing the trade-offs here"},
	)
	if !strings.Contains(out, "thinking") {
		t.Fatalf("reasoning should create a thinking row:\n%s", out)
	}
	if strings.Contains(out, "weighing the trade-offs here") {
		t.Fatalf("collapsed reasoning leaked into the response body:\n%s", out)
	}
}

func TestTimelineAppendThinkingDoesNotReparseTags(t *testing.T) {
	out := drive(t,
		teasink.ConversationStartedMsg{TaskID: "t2", Prompt: "question"},
		teasink.ThinkingMsg{TaskID: "t2", Delta: "consider the <think> sentinel literally"},
	)
	if !strings.Contains(out, "thinking") {
		t.Fatalf("literal tag reasoning should remain a thinking row:\n%s", out)
	}
}
