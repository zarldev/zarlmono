package tui_test

import (
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
)

func TestSubagentSpawnCorrelatesReservedRow(t *testing.T) {
	out := drive(t, teasink.ToolStartedMsg{TaskID: "root", ToolID: "spawn", ToolName: "agent_spawn", Parameters: map[string]any{"agent": "reviewer", "prompt": "review"}}, teasink.ConversationStartedMsg{TaskID: "child", Depth: 1, ParentToolCallID: "spawn", AgentName: "reviewer", Prompt: "review"})
	if strings.Count(out, "reviewer: review") != 1 {
		t.Fatalf("spawn row not correlated:\n%s", out)
	}
}

func TestTeaSinkProjectsOneSubagentRowPerExactSpawnExecution(t *testing.T) {
	var mu sync.Mutex
	var projected []tea.Msg
	sink := teasink.New(func(message tea.Msg) {
		mu.Lock()
		projected = append(projected, message)
		mu.Unlock()
	})
	t.Cleanup(sink.Close)

	for _, spawn := range []struct {
		executionID string
		taskID      string
		agent       string
		prompt      string
	}{
		{executionID: "execution-A", taskID: "child-A", agent: "reviewer", prompt: "review A"},
		{executionID: "execution-B", taskID: "child-B", agent: "tester", prompt: "test B"},
	} {
		sink.OnToolStarted(runner.ToolStarted{
			TaskID: taskscope.ID("root"), ExecutionID: spawn.executionID, ToolID: "reused", ToolName: "agent_spawn",
			Parameters: map[string]any{"agent": spawn.agent, "prompt": spawn.prompt},
		})
		sink.OnConversationStarted(runner.ConversationStarted{
			TaskID: taskscope.ID(spawn.taskID), Depth: 1, ParentExecutionID: spawn.executionID, ParentToolCallID: "reused",
			AgentName: spawn.agent, Prompt: spawn.prompt,
		})
	}
	sink.Drain()

	mu.Lock()
	messages := append([]tea.Msg(nil), projected...)
	mu.Unlock()
	if len(messages) != 4 {
		t.Fatalf("projected messages = %d, want 4", len(messages))
	}
	out := drive(t, messages...)
	for _, want := range []string{"reviewer: review A", "tester: test B"} {
		if count := strings.Count(out, want); count != 1 {
			t.Fatalf("visible subsection %q count = %d, want 1:\n%s", want, count, out)
		}
	}
	if count := strings.Count(out, "agents (2)"); count != 1 {
		t.Fatalf("agents section count = %d, want 1:\n%s", count, out)
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
