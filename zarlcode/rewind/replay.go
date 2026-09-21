package rewind

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// providerMessageValid checks the fields the selected outbound adapter actually
// consumes. Keeping a field in storage is not evidence it survives replay.
func providerMessageValid(message llm.Message, provider string) bool {
	// Display reasoning needs a supported native continuation, not necessarily
	// an identical summary: Responses also streams raw reasoning text.
	if message.ReasoningContent != "" && !nativeReasoningReplayValid(message, provider) {
		return false
	}
	if message.Role == llm.RoleUser {
		if provider == providerAnthropic {
			return message.Content != "" || len(message.Parts) != 0
		}
		// The runner retains the prompt in Content and in the first text part;
		// later parts may contain additional text or image attachments. Adapters
		// consume Parts, so the redundant prompt must match without rewriting it.
		return message.Content == "" || len(message.Parts) == 0 ||
			(message.Parts[0].Type == llm.ContentTypeText && message.Parts[0].Text == message.Content)
	}
	if message.Role == llm.RoleTool {
		return provider != providerCodex || message.Content != ""
	}
	return outputOrderValid(message, provider)
}

func outputOrderValid(message llm.Message, provider string) bool {
	indexes := make([]*int, 0, len(message.ContinuationItems)+len(message.ToolCalls)+1)
	var nativeText []llm.ContinuationItem
	for _, item := range message.ContinuationItems {
		indexes = append(indexes, item.OutputIndex)
		if (provider == providerAnthropic && item.Kind == continuationText) ||
			((provider == providerOpenAI || provider == providerCodex) && item.Kind == "message") {
			nativeText = append(nativeText, item)
		}
	}
	switch {
	case len(nativeText) > 0:
		if !nativeTextMatches(message, nativeText) {
			return false
		}
	case message.Content != "":
		indexes = append(indexes, message.ContentOutputIndex)
	case message.ContentOutputIndex != nil:
		return false
	}
	for _, call := range message.ToolCalls {
		indexes = append(indexes, call.OutputIndex)
	}
	seen := make(map[int]bool, len(indexes))
	missing := 0
	for _, index := range indexes {
		if index == nil {
			missing++
			continue
		}
		// Positions order retained output; they are not offsets into this slice.
		// Provider streams can leave gaps between replayable items.
		if *index < 0 || seen[*index] {
			return false
		}
		seen[*index] = true
	}
	// Unindexed ordinary messages retain slice order. Native/projected mixtures
	// require explicit positions, rather than relying on adapter fallback order.
	return missing == 0 || (len(seen) == 0 && (len(message.ContinuationItems) == 0 || len(indexes) == 1))
}

func nativeTextMatches(message llm.Message, items []llm.ContinuationItem) bool {
	for _, item := range items {
		if item.OutputIndex == nil {
			return false
		}
	}
	sort.Slice(items, func(i, j int) bool { return *items[i].OutputIndex < *items[j].OutputIndex })
	var text strings.Builder
	for _, item := range items {
		var block struct {
			Text    string `json:"text"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(item.Data, &block) != nil {
			return false
		}
		if item.Provider == providerOpenAI || item.Provider == providerCodex {
			for _, part := range block.Content {
				if part.Type == "output_text" {
					text.WriteString(part.Text)
				}
			}
			continue
		}
		text.WriteString(block.Text)
	}
	// Native blocks own replay text. Older runners trimmed the display projection
	// before retaining it; admit that exact historical transformation without
	// changing either representation or accepting different interior content.
	native := text.String()
	trimmed := strings.TrimSpace(native)
	return (message.Content == "" || message.Content == native || message.Content == trimmed) &&
		(message.ContentOutputIndex == nil || ((message.Content != "" || trimmed == "") && *message.ContentOutputIndex == *items[0].OutputIndex))
}
