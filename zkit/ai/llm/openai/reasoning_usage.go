package openai

import (
	"encoding/json"

	sdk "github.com/openai/openai-go/v3"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func chatUsage(u sdk.CompletionUsage) llm.Usage {
	usage := llm.Usage{
		PromptTokens: int(u.PromptTokens), CompletionTokens: int(u.CompletionTokens),
		TotalTokens: int(u.TotalTokens), CachedTokens: int(u.PromptTokensDetails.CachedTokens),
	}
	// The SDK's numeric zero alone cannot distinguish omission from zero.
	var details struct {
		Completion struct {
			Reasoning *int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	}
	if json.Unmarshal([]byte(u.RawJSON()), &details) == nil && details.Completion.Reasoning != nil {
		usage.ReasoningTokens = *details.Completion.Reasoning
		usage.ReasoningTokensReported = true
	}
	return usage
}
