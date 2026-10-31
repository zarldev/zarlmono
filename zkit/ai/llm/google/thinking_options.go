package google

import (
	"errors"
	"fmt"
	"strings"

	"google.golang.org/genai"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// applyThinkingOptions applies explicit Gemini thinking controls to the request.
// An unspecified budget is not an explicit request to disable thinking.
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
		cfg.ThinkingConfig.ThinkingBudget = new(int32(tokens)) // #nosec G115 -- model budget <= 32768.
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

const (
	thinkingFlash     = "gemini-2.5-flash"
	thinkingFlashLite = "gemini-2.5-flash-lite"
	thinkingPro       = "gemini-2.5-pro"
)

func googleBudgetRange(model string) (int, int, bool) {
	switch model {
	case thinkingFlash, thinkingFlashLite:
		if model == thinkingFlashLite {
			return 512, 24576, true
		}
		return 1, 24576, true
	case thinkingPro:
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
