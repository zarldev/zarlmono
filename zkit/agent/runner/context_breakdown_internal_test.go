package runner

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestComputeContextBreakdown(t *testing.T) {
	msgs := []llm.Message{
		{Role: "system", Content: "sys-prompt"},        // 10
		{Role: "user", Content: "do the thing please"}, // 19
		{Role: "assistant", Content: "ok", ToolCalls: []llm.ToolCall{
			{ID: "c1", Function: llm.ToolCallFunction{Name: "load_skill"}},
			{ID: "c2", Function: llm.ToolCallFunction{Name: "spawn_agent"}},
			{ID: "c3", Function: llm.ToolCallFunction{Name: "bash"}},
		}},
		{Role: "tool", ToolCallID: "c1", Content: "skill body content"}, // skill
		{Role: "tool", ToolCallID: "c2", Content: "agent summary"},      // agent
		{Role: "tool", ToolCallID: "c3", Content: "$ ls"},               // other
	}

	b := computeContextBreakdown(msgs)

	if b.SystemMsgs != 1 || b.UserMsgs != 1 || b.AssistantMsgs != 1 || b.ToolMsgs != 3 {
		t.Errorf("msg counts = sys%d usr%d asst%d tool%d", b.SystemMsgs, b.UserMsgs, b.AssistantMsgs, b.ToolMsgs)
	}
	if b.SystemBytes != len("sys-prompt") || b.UserBytes != len("do the thing please") {
		t.Errorf("sys/user bytes = %d/%d", b.SystemBytes, b.UserBytes)
	}
	// A tool message's footprint includes its ToolCallID (it's part of the
	// wire payload that occupies context), so skill bytes = content + "c1".
	if want := len("skill body content") + len("c1"); b.SkillBytes != want {
		t.Errorf("skill bytes = %d, want %d", b.SkillBytes, want)
	}
	if want := len("agent summary") + len("c2"); b.AgentBytes != want {
		t.Errorf("agent bytes = %d, want %d", b.AgentBytes, want)
	}
	// ToolBytes is the sum of all three tool results; skill+agent are a
	// subset, so "other" (bash) is the remainder.
	other := b.ToolBytes - b.SkillBytes - b.AgentBytes
	if want := len("$ ls") + len("c3"); other != want {
		t.Errorf("other tool bytes = %d, want %d", other, want)
	}
	// Assistant bytes include the tool-call function names + the content.
	wantAsst := len("ok") + len("load_skill") + len("spawn_agent") + len("bash")
	if b.AssistantBytes != wantAsst {
		t.Errorf("assistant bytes = %d, want %d", b.AssistantBytes, wantAsst)
	}
}

func TestComputeContextBreakdownEmpty(t *testing.T) {
	if b := computeContextBreakdown(nil); b != (ContextBreakdown{}) {
		t.Errorf("empty history should yield zero breakdown, got %+v", b)
	}
}

func TestComputeContextBreakdownOrphanTool(t *testing.T) {
	// A tool result whose originating assistant call was compacted away
	// still counts toward ToolBytes, just not skill/agent.
	msgs := []llm.Message{{Role: "tool", ToolCallID: "gone", Content: "orphaned"}}
	b := computeContextBreakdown(msgs)
	if want := len("orphaned") + len("gone"); b.ToolBytes != want || b.SkillBytes != 0 || b.AgentBytes != 0 {
		t.Errorf("orphan tool = tool%d skill%d agent%d (want tool%d)", b.ToolBytes, b.SkillBytes, b.AgentBytes, want)
	}
}
