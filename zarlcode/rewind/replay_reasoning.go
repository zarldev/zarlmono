package rewind

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// reasoningReplayValid follows each adapter's live reasoning representation.
// Responses may expose raw reasoning or a partial display summary independently
// of the encrypted item. Anthropic's thinking projects its signed block.
func reasoningReplayValid(message llm.Message, provider string) bool {
	// Ordinary replay follows the adapter's live policy: local adapters rewrap
	// inline thinking, DeepSeek strips or trims it by model/request, and Google
	// and Claude Code keep it display-only. Never erase the stored projection.
	switch provider {
	case providerDeepSeek, providerLlamaCPP, providerOllama, providerGemini, providerGoogleVertex, providerClaudeCode:
		return true
	}
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
