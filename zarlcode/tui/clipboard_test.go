package tui_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestCopyLastResponseFindsNestedSubagentReply(t *testing.T) {
	model := tui.New()
	updated, _ := model.Update(teasink.ConversationStartedMsg{TaskID: "root", Prompt: "main"})
	model = updated.(*tui.UI)
	updated, _ = model.Update(teasink.ContentMsg{TaskID: "root", Delta: "older top-level response"})
	model = updated.(*tui.UI)
	updated, _ = model.Update(teasink.ConversationStartedMsg{TaskID: "child", Depth: 1, AgentName: "reviewer", Prompt: "review"})
	model = updated.(*tui.UI)
	updated, _ = model.Update(teasink.ContentMsg{TaskID: "child", Depth: 1, Delta: "newest nested response"})
	model = updated.(*tui.UI)

	if got := model.LatestAssistantResponse(); got != "newest nested response" {
		t.Fatalf("latest assistant response = %q, want nested reply", got)
	}
}
