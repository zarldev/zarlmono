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
