package rewind

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// Native reasoning is required for replay. Responses streams may expose raw
// reasoning or a partial summary on the display channel, independently of the
// completed encrypted item's summary. Preserve both without equating them.
// Anthropic's display thinking is a projection of its signed thinking block.
func nativeReasoningReplayValid(message llm.Message, provider string) bool {
	var items []llm.ContinuationItem
	for _, item := range message.ContinuationItems {
		if (item.Kind == continuationReasoning || item.Kind == continuationThinking) && continuationValid(item, provider) {
			items = append(items, item)
		}
	}
	if len(items) == 0 {
		return false
	}
	if provider == providerOpenAI || provider == providerCodex {
		return true
	}
	if len(items) > 1 {
		for _, item := range items {
			if item.OutputIndex == nil {
				return false
			}
		}
		sort.Slice(items, func(i, j int) bool { return *items[i].OutputIndex < *items[j].OutputIndex })
	}
	var text strings.Builder
	hasPlaintext := false
	for _, item := range items {
		var native struct {
			Thinking *string `json:"thinking"`
		}
		if json.Unmarshal(item.Data, &native) != nil {
			return false
		}
		if native.Thinking != nil {
			hasPlaintext = true
			text.WriteString(*native.Thinking)
		}
	}
	return !hasPlaintext || message.ReasoningContent == text.String()
}
