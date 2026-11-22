package google

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/genai"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// ErrInvalidThinkingConfig identifies explicit thinking controls unsupported by
// the requested model, invalid control values, and conflicting controls.
var ErrInvalidThinkingConfig = errors.New("invalid google thinking configuration")

// thinkingLevelXHigh is the documented GenerateContent level for Hydrogen.
// genai v1.76.0 serializes it on both backends but has no named constant for it.
const thinkingLevelXHigh genai.ThinkingLevel = "XHIGH"

const (
	thinkingFlash     = "gemini-2.5-flash"
	thinkingFlashLite = "gemini-2.5-flash-lite"
	thinkingPro       = "gemini-2.5-pro"
	thinkingFlash35   = "gemini-3.5-flash"
	thinkingFlash38   = "gemini-3.8-flash"
	thinkingArgon     = "gemini-4-argon"
)

type thinkingPolicy struct {
	minimum, maximum int
	off              bool
	levels           []genai.ThinkingLevel
}

// modelThinkingPolicy recognizes exact IDs only; models/ is a lookup prefix,
// not a rewrite of the ID passed to the SDK.
func modelThinkingPolicy(model string) thinkingPolicy {
	switch strings.TrimPrefix(model, "models/") {
	case thinkingFlash:
		return thinkingPolicy{minimum: 1, maximum: 24576, off: true}
	case thinkingPro:
		return thinkingPolicy{minimum: 128, maximum: 32768}
	case thinkingFlashLite:
		return thinkingPolicy{minimum: 512, maximum: 24576, off: true}
	case "gemini-3-pro-preview":
		return thinkingPolicy{levels: []genai.ThinkingLevel{genai.ThinkingLevelLow, genai.ThinkingLevelHigh}}
	case thinkingFlash38, thinkingArgon, "gemini-4-carbon", "gemini-3.1-pro-preview":
		return thinkingPolicy{levels: []genai.ThinkingLevel{genai.ThinkingLevelLow, genai.ThinkingLevelMedium, genai.ThinkingLevelHigh}}
	case "gemini-4-hydrogen":
		return thinkingPolicy{levels: []genai.ThinkingLevel{genai.ThinkingLevelLow, genai.ThinkingLevelMedium, genai.ThinkingLevelHigh, thinkingLevelXHigh}}
	case "gemini-3.6-flash", thinkingFlash35, "gemini-3.5-flash-lite",
		"gemini-3-flash-preview", "gemini-3.1-flash-lite-preview", "gemini-3.1-flash-lite":
		return thinkingPolicy{levels: []genai.ThinkingLevel{genai.ThinkingLevelMinimal, genai.ThinkingLevelLow, genai.ThinkingLevelMedium, genai.ThinkingLevelHigh}}
	default:
		return thinkingPolicy{}
	}
}

// applyThinkingOptions selects, validates, and emits both raw and typed controls.
// Typed zero is omission; raw presence is explicit and takes budget precedence.
func (p *Provider) applyThinkingOptions(cfg *genai.GenerateContentConfig, req llm.CompletionRequest) error {
	rawBudget, hasRawBudget := req.Options["thinking_budget"]
	rawLevel, hasLevel := req.Options["thinking_level"]
	hasBudget := hasRawBudget || req.Thinking.BudgetTokens != 0
	if hasBudget && hasLevel {
		return fmt.Errorf("google: thinking_budget and thinking_level conflict: %w", ErrInvalidThinkingConfig)
	}
	policy := modelThinkingPolicy(p.model)
	if hasBudget {
		tokens := req.Thinking.BudgetTokens
		if hasRawBudget {
			var ok bool
			tokens, ok = rawBudget.(int)
			if !ok {
				return fmt.Errorf("google: thinking_budget: %w", ErrInvalidThinkingConfig)
			}
		}
		validBudget := tokens == -1 || tokens == 0 && policy.off || tokens >= policy.minimum && tokens <= policy.maximum
		if policy.maximum == 0 || !validBudget {
			return fmt.Errorf("google: thinking_budget: %w", ErrInvalidThinkingConfig)
		}
		cfg.ThinkingConfig.ThinkingBudget = new(int32(tokens)) // #nosec G115 -- validated budget is -1..32768.
	}
	if hasLevel {
		value, ok := rawLevel.(string)
		level := genai.ThinkingLevel(strings.ToUpper(strings.TrimSpace(value)))
		if !ok || !slices.Contains(policy.levels, level) {
			return fmt.Errorf("google: thinking_level: %w", ErrInvalidThinkingConfig)
		}
		cfg.ThinkingConfig.ThinkingLevel = level
	}
	return nil
}
