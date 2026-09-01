package tui_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestTimelineGroupsSubAgentsUnderOneAgentsSection(t *testing.T) {
	out := drive(t,
		teasink.ToolStartedMsg{TaskID: "root", ToolID: "a1", ToolName: "agent_spawn", Parameters: map[string]any{"agent": "reviewer", "prompt": "review changes"}},
		teasink.ConversationStartedMsg{TaskID: "first", Depth: 1, ParentToolCallID: "a1", AgentName: "reviewer", Prompt: "review changes"},
		teasink.ToolStartedMsg{TaskID: "root", ToolID: "a2", ToolName: "agent_spawn", Parameters: map[string]any{"agent": "tester", "prompt": "run tests"}},
		teasink.ConversationStartedMsg{TaskID: "second", Depth: 1, ParentToolCallID: "a2", AgentName: "tester", Prompt: "run tests"},
	)
	for _, want := range []string{"agents (2)", "reviewer: review changes", "tester: run tests"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}
