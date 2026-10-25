package google

import (
	"errors"
	"fmt"
	"strings"

	"google.golang.org/genai"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// applyThinkingOptions handles explicit Gemini controls. The neutral zero
// ThinkingConfig remains unspecified, not a request to disable thinking.
// thinking_budget accepts an int (zero explicitly disables supported Flash
// models); thinking_level accepts a supported level string. They are exclusive.
func (p *Provider) applyThinkingOptions(cfg *genai.GenerateContentConfig, req llm.CompletionRequest) error {
	budget, hasBudget := req.Options["thinking_budget"]
	level, hasLevel := req.Options["thinking_level"]
	if hasBudget && hasLevel || hasLevel && req.Thinking.BudgetTokens > 0 {
		return errors.New("google: thinking_budget and thinking_level are mutually exclusive")
	}
	model := strings.TrimPrefix(p.model, "models/")
	if hasBudget {
		tokens, ok := budget.(int)
		minimum, maximum, off := googleBudgetRange(model)
		if !ok || maximum == 0 || (tokens != 0 || !off) && (tokens < minimum || tokens > maximum) {
			return fmt.Errorf("google: unsupported thinking_budget for model %q", model)
		}
		cfg.ThinkingConfig.ThinkingBudget = new(int32(tokens)) // #nosec G115 -- validated model budget is in [0, 32768].
	}
	if hasLevel {
		value, ok := level.(string)
		value = strings.ToUpper(strings.TrimSpace(value))
		if !ok || !googleThinkingLevel(model, value) {
			return fmt.Errorf("google: unsupported thinking_level for model %q", model)
		}
		cfg.ThinkingConfig.ThinkingBudget = nil
		cfg.ThinkingConfig.ThinkingLevel = genai.ThinkingLevel(value)
	}
	return nil
}

func googleBudgetRange(model string) (int, int, bool) {
	switch model {
	case "gemini-2.5-flash", "gemini-2.5-flash-lite":
		if model == "gemini-2.5-flash-lite" {
			return 512, 24576, true
		}
		return 1, 24576, true
	case "gemini-2.5-pro":
		return 128, 32768, false
	default:
		return 0, 0, false
	}
}

func googleThinkingLevel(model, level string) bool {
	value := genai.ThinkingLevel(level)
	switch model {
	case "gemini-3-pro-preview":
		return value == genai.ThinkingLevelLow || value == genai.ThinkingLevelHigh
	case "gemini-3.1-pro-preview":
		return value == genai.ThinkingLevelLow || value == genai.ThinkingLevelMedium || value == genai.ThinkingLevelHigh
	case "gemini-3-flash-preview", "gemini-3.1-flash-lite-preview", "gemini-3.1-flash-lite":
		return value == genai.ThinkingLevelMinimal || value == genai.ThinkingLevelLow || value == genai.ThinkingLevelMedium || value == genai.ThinkingLevelHigh
	default:
		return false
	}
}
