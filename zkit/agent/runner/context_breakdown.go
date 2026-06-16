package runner

import "github.com/zarldev/zarlmono/zkit/ai/llm"

// Registered tool names whose results get their own slice of the tool
// breakdown. Held as literals rather than imported from the tool packages
// to avoid an import cycle (spawn imports the runner). They are the names
// the model sees, so they're stable wire contract, not internal detail.
const (
	toolNameLoadSkill  = "load_skill"
	toolNameSpawnAgent = "spawn_agent"
)

// computeContextBreakdown walks a Run's working history once and tallies
// per-role bytes + message counts, attributing each tool-result message to
// the tool that produced it (by matching ToolCallID against the assistant
// ToolCalls that requested it) so load_skill / spawn_agent content can be
// split out. It's O(n) over the slice — the same order the iteration loop
// already pays — and allocates one small id→name map.
func computeContextBreakdown(msgs []llm.Message) ContextBreakdown {
	var b ContextBreakdown
	if len(msgs) == 0 {
		return b
	}

	// First pass: map every tool-call ID to the tool name that issued it.
	idToName := make(map[string]string, len(msgs))
	for i := range msgs {
		for _, tc := range msgs[i].ToolCalls {
			if tc.ID != "" && tc.Function.Name != "" {
				idToName[tc.ID] = tc.Function.Name
			}
		}
	}

	for i := range msgs {
		m := &msgs[i]
		n := msgBytes(m)
		switch m.Role {
		case llm.RoleSystem:
			b.SystemBytes += n
			b.SystemMsgs++
		case llm.RoleUser:
			b.UserBytes += n
			b.UserMsgs++
		case llm.RoleAssistant:
			b.AssistantBytes += n
			b.AssistantMsgs++
		case llm.RoleTool:
			b.ToolBytes += n
			b.ToolMsgs++
			switch idToName[m.ToolCallID] {
			case toolNameLoadSkill:
				b.SkillBytes += n
			case toolNameSpawnAgent:
				b.AgentBytes += n
			}
		}
	}
	return b
}

// msgBytes is one message's footprint in the context window: content +
// reasoning + multimodal parts + tool-call payload + tool-call id. Mirrors
// the v1 consumer-side estimate so the v1 and v2 graphs agree.
func msgBytes(m *llm.Message) int {
	total := len(m.Content) + len(m.ReasoningContent)
	for _, p := range m.Parts {
		total += len(p.Text)
		if p.Image != nil {
			total += len(p.Image.DataURI) + len(p.Image.URL)
		}
		if p.Audio != nil {
			total += len(p.Audio.DataURI)
		}
	}
	for _, tc := range m.ToolCalls {
		total += len(tc.Function.Name) + len(tc.Function.Arguments)
	}
	total += len(m.ToolCallID)
	return total
}
