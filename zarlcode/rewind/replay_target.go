package rewind

import (
	"fmt"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

// Bind Responses-only fields to the saved model's actual streaming route, not
// merely its provider name. Custom endpoints are excluded by runtime capture.
func validateTargetContext(messages []llm.Message, target Target) error {
	if err := ValidateContext(messages, target.Provider); err != nil {
		return err
	}
	if target.Provider != providerOpenAI || openai.SupportsResponsesReplay(target.Model) {
		return nil
	}
	for _, message := range messages {
		if len(message.ContinuationItems) != 0 || message.ContentOutputIndex != nil {
			return fmt.Errorf("%w: saved model does not replay Responses metadata", ErrInvalid)
		}
		for _, call := range message.ToolCalls {
			if call.OutputIndex != nil {
				return fmt.Errorf("%w: saved model does not replay Responses tool positions", ErrInvalid)
			}
		}
	}
	return nil
}
