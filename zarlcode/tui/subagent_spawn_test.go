package tui_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestSubagentSpawnCorrelatesReservedRow(t *testing.T) {
	out := drive(t, teasink.ToolStartedMsg{TaskID: "root", ToolID: "spawn", ToolName: "agent_spawn", Parameters: map[string]any{"agent": "reviewer", "prompt": "review"}}, teasink.ConversationStartedMsg{TaskID: "child", Depth: 1, ParentToolCallID: "spawn", AgentName: "reviewer", Prompt: "review"})
	if strings.Count(out, "reviewer: review") != 1 {
		t.Fatalf("spawn row not correlated:\n%s", out)
	}
}
func TestSubagentSpawnFailureRemainsVisible(t *testing.T) {
	out := drive(t, teasink.ToolStartedMsg{TaskID: "root", ToolID: "spawn", ToolName: "agent_spawn", Parameters: map[string]any{"agent": "reviewer", "prompt": "review"}}, teasink.ToolFailedMsg{TaskID: "root", ToolID: "spawn", ToolName: "agent_spawn", Error: "denied"})
	if !strings.Contains(out, "agents (1)") {
		t.Fatalf("reserved agent row missing:\n%s", out)
	}
}

func TestSubagentRowsPreserveNestedDepth(t *testing.T) {
	out := drive(t,
		teasink.ConversationStartedMsg{TaskID: "root", Depth: 0},
		teasink.ToolStartedMsg{TaskID: "root", Depth: 0, ToolID: "spawn-child", ToolName: "agent_spawn", Parameters: map[string]any{"agent": "child", "prompt": "child task"}},
		teasink.ConversationStartedMsg{TaskID: "child", Depth: 1, ParentToolCallID: "spawn-child", AgentName: "child", Prompt: "child task"},
		teasink.ToolStartedMsg{TaskID: "child", Depth: 1, ToolID: "spawn-grandchild", ToolName: "agent_spawn", Parameters: map[string]any{"agent": "grandchild", "prompt": "grandchild task"}},
		teasink.ConversationStartedMsg{TaskID: "grandchild", Depth: 2, ParentToolCallID: "spawn-grandchild", AgentName: "grandchild", Prompt: "grandchild task"},
	)
	if !strings.Contains(out, "grandchild: grandchild task") {
		t.Fatalf("nested sub-agent row missing:\n%s", out)
	}
	if !strings.Contains(out, "⇢ ⇢") {
		t.Fatalf("nested sub-agent depth was not rendered:\n%s", out)
	}
}
