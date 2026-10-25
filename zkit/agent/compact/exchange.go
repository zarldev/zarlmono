package compact

import "github.com/zarldev/zarlmono/zkit/ai/llm"

// KeepRecentExchange expands a positive keep count to protect the latest
// assistant tool exchange, including results and any subsequently admitted
// input. A message-count limit must not discard results before the next request
// can consume them. Zero deliberately leaves the entire history eligible.
func KeepRecentExchange(history []llm.Message, keep int) int {
	if keep <= 0 || keep >= len(history) {
		return keep
	}
	cut := len(history) - keep
	// Also snap a boundary that lands inside an older tool batch.
	for cut > 0 && history[cut].Role == llm.RoleTool {
		cut--
	}
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == llm.RoleAssistant {
			if len(history[i].ToolCalls) > 0 {
				cut = min(cut, i)
			}
			break
		}
	}
	return len(history) - cut
}
