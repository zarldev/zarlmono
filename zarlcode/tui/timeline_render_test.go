package tui_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestTimelineRenderShowsUserAssistantAndTools(t *testing.T) {
	out := drive(t, teasink.ConversationStartedMsg{TaskID: "t", Prompt: "question"}, teasink.ContentMsg{TaskID: "t", Delta: "answer"}, teasink.ToolStartedMsg{TaskID: "t", ToolID: "c", ToolName: "read"})
	for _, want := range []string{"question", "answer", "tools (1)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}
