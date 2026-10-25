package anthropic

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/options"
)

// WithHTTPClient borrows the caller's HTTP client for SDK requests. The provider
// neither mutates nor closes it; its timeout/transport policy remains caller-owned.
func WithHTTPClient(client *http.Client) options.Option[Provider] {
	return func(p *Provider) { p.httpClient = client }
}

// messageParams owns request shaping for both transport modes. It is invoked
// only during stream iteration, including validation of external native items.
func (p *Provider) messageParams(req llm.CompletionRequest) (anthropic.MessageNewParams, error) {
	for _, message := range req.Messages {
		for _, part := range message.Parts {
			if part.Type != llm.ContentTypeText && part.Type != llm.ContentTypeImage {
				return anthropic.MessageNewParams{}, fmt.Errorf("anthropic: %w: unsupported %s input", llm.ErrModelNotSupported, part.Type)
			}
		}
		for _, item := range message.ContinuationItems {
			if item.Provider != continuationProvider || item.Format != continuationFormat {
				continue
			}
			var block nativeThinkingBlock
			if err := json.Unmarshal(item.Data, &block); err != nil {
				return anthropic.MessageNewParams{}, fmt.Errorf("parse anthropic continuation: %w", err)
			}
			if (item.Kind != "" && item.Kind != block.Type) || (item.OutputIndex != nil && *item.OutputIndex < 0) ||
				(block.Type != continuationText && block.Type != continuationThinking && block.Type != continuationRedacted) {
				return anthropic.MessageNewParams{}, errors.New("anthropic continuation: unsupported or contradictory block")
			}
		}
	}
	messages := convertMessagesToSDK(req.Messages)
	setLastMessageCacheBreakpoint(messages)
	model := wireModelID(p.model)
	params := anthropic.MessageNewParams{Model: model, Messages: messages, MaxTokens: int64(getMaxTokens(req.MaxTokens))}
	if req.Temperature > 0 && !req.Thinking.Enabled && customTemperatureModel(model) {
		params.Temperature = anthropic.Float(float64(req.Temperature))
	}
	if system := extractSystemPrompt(req.Messages); system != "" {
		params.System = []anthropic.TextBlockParam{{Text: system, CacheControl: staticCacheControl()}}
	}
	params.Tools = convertToolsToSDK(req.Tools)
	applyResponseFormat(&params, req.ResponseFormat)
	applyThinking(&params, req.Thinking, req.Options)
	if effort, ok := req.Options["reasoning_effort"].(string); ok && effort != "" {
		if !validModelEffort(model, effort) {
			return anthropic.MessageNewParams{}, fmt.Errorf("anthropic: %w: effort %q is unsupported for %s", llm.ErrModelNotSupported, effort, model)
		}
		params.OutputConfig.Effort = anthropic.OutputConfigEffort(effort)
	}
	if tier, ok := req.Options["service_tier"].(string); ok && tier != "" {
		if tier != "auto" && tier != "standard_only" {
			return anthropic.MessageNewParams{}, fmt.Errorf("anthropic: unsupported service tier %q", tier)
		}
		params.ServiceTier = anthropic.MessageNewParamsServiceTier(tier)
	}
	return params, nil
}

func adaptiveThinkingModel(model string) bool {
	model = wireModelID(model)
	return strings.HasPrefix(model, modelClaudeOpus46) || strings.HasPrefix(model, modelClaudeOpus47) ||
		strings.HasPrefix(model, modelClaudeSonnet46) || strings.Contains(model, "-5-")
}

func customTemperatureModel(model string) bool {
	return !adaptiveThinkingModel(model) || strings.HasPrefix(model, modelClaudeOpus46)
}

func validModelEffort(model, effort string) bool {
	switch effort {
	case "low", "medium", "high":
		return adaptiveThinkingModel(model) || strings.HasPrefix(model, "claude-opus-4-5")
	case "xhigh":
		return strings.HasPrefix(model, modelClaudeOpus47) || strings.Contains(model, "-5-")
	case "max":
		return strings.HasPrefix(model, modelClaudeOpus46) || strings.HasPrefix(model, modelClaudeOpus47) || strings.Contains(model, "-5-")
	default:
		return false
	}
}

// Legacy picker suffixes are host aliases, never literal API model identifiers.
// Only known models whose native window is 1M accept these aliases.
func wireModelID(model string) string {
	base := strings.TrimSuffix(strings.TrimSuffix(model, "[1m]"), "-1m")
	switch base {
	case modelClaudeOpus47, modelClaudeOpus46, modelClaudeSonnet46:
		return base
	default:
		return model
	}
}
