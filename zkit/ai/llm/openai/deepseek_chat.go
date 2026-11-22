package openai

import (
	"encoding/json"
	"errors"
	"strings"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/options"
)

// ErrDeepSeekChatControl identifies an invalid explicit native Chat control.
// Raw option values and parser diagnostics are not exposed by this error.
var ErrDeepSeekChatControl = errors.New("invalid deepseek chat control")

// WithDeepSeekChatDialect enables native DeepSeek Chat thinking/effort mapping
// and cache-hit aliases, independently of endpoint URL and model spelling.
// It does not affect Responses, enable thinking automatically, forward arbitrary
// options, or select any gateway behavior. Explicit thinking.type takes authority
// over reasoning_effort for mode selection, so enabled plus none retains both.
func WithDeepSeekChatDialect(enabled bool) options.Option[Provider] {
	return func(p *Provider) { p.deepSeekChatDialect = enabled }
}

func deepSeekChatOptions(req llm.CompletionRequest) ([]option.RequestOption, error) {
	var opts []option.RequestOption
	if req.Thinking.Enabled {
		opts = append(opts, option.WithJSONSet("thinking", struct {
			Type string `json:"type"`
		}{Type: "enabled"}))
	}
	if raw, present := req.Options["reasoning_effort"]; present {
		text, ok := raw.(string)
		if !ok {
			return nil, ErrDeepSeekChatControl
		}
		text = strings.TrimSpace(text)
		// DeepSeek documents minimal as a compatibility spelling of low;
		// medium and xhigh are accepted by the shared enum, then mapped below.
		if text == "minimal" {
			text = ReasoningEfforts.REASONINGEFFORTLOW.String()
		}
		effort, err := ParseReasoningEffort(text)
		if err != nil {
			return nil, ErrDeepSeekChatControl
		}
		switch effort {
		case ReasoningEfforts.REASONINGEFFORTMEDIUM, ReasoningEfforts.REASONINGEFFORTXHIGH:
			effort = ReasoningEfforts.REASONINGEFFORTHIGH
		}
		opts = append(opts, option.WithJSONSet("reasoning_effort", effort.String()))
	}
	return opts, nil
}

func (p *Provider) chatUsage(u sdk.CompletionUsage) llm.Usage {
	usage := chatUsage(u)
	if !p.deepSeekChatDialect {
		return usage
	}
	// Presence is significant: canonical explicit zero wins over the alias.
	// A malformed present canonical counter must not unlock a fallback claim.
	var raw struct {
		Details json.RawMessage `json:"prompt_tokens_details"`
		Hit     json.RawMessage `json:"prompt_cache_hit_tokens"`
	}
	usage.CachedTokens = 0
	if json.Unmarshal([]byte(u.RawJSON()), &raw) != nil {
		return usage
	}
	var details struct {
		Cached json.RawMessage `json:"cached_tokens"`
	}
	if json.Unmarshal(raw.Details, &details) == nil {
		if len(details.Cached) != 0 && string(details.Cached) != "null" {
			usage.CachedTokens = deepSeekCachedTokens(details.Cached, usage.PromptTokens)
			return usage
		}
	} else if len(raw.Details) != 0 {
		return usage
	}
	usage.CachedTokens = deepSeekCachedTokens(raw.Hit, usage.PromptTokens)
	return usage
}

func deepSeekCachedTokens(raw json.RawMessage, promptTokens int) int {
	// Hits are a subset of the canonical prompt count, never additive. Decode
	// directly into int to reject fractions, strings, and platform overflow.
	var count int
	if json.Unmarshal(raw, &count) != nil || count < 0 || count > promptTokens {
		return 0
	}
	return count
}
