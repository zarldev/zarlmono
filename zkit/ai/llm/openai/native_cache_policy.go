package openai

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// PromptCacheOption selects native OpenAI request cache policy through
// CompletionRequest.Options. Its JSON-like object requires mode (implicit or
// explicit), and accepts optional ttl and []any breakpoints. Each breakpoint
// requires message_index and content_index referring to the final request's
// original system/user text, before tool attachment expansion, and optional ttl.
// Chat accepts 30m; Responses accepts 30m or 12h. Omitted TTL inherits the
// service/request default. Explicit mode without breakpoints disables cache use.
// At most four explicit or three implicit-mode selectors are accepted; these
// local limits do not guarantee cacheability or cache hits. Custom endpoints and
// gateways are unsupported. Legacy retention cannot coexist; cache keys can.
// Omission preserves legacy behavior. Validation occurs only during iteration.
const PromptCacheOption = "openai_prompt_cache"

// ErrInvalidCachePolicy identifies malformed, conflicting or unsupported native
// cache policy. Errors wrapping it contain fixed reasons, never caller data.
var ErrInvalidCachePolicy = errors.New("openai: invalid prompt cache policy")

type nativeCachePolicy struct {
	mode        NativeCacheMode
	ttl         NativeCacheTTL
	breakpoints []nativeCacheSelector
}

type nativeCacheSelector struct {
	message int
	content int
	ttl     NativeCacheTTL
}

func invalidCachePolicy(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidCachePolicy, reason)
}

func cachePolicyObject(raw any) (map[string]any, bool) {
	switch raw := raw.(type) {
	case map[string]any:
		return raw, raw != nil
	case llm.ModelOptions:
		return raw, raw != nil
	default:
		return nil, false
	}
}

func (p *Provider) resolveNativeCachePolicy(raw any, req llm.CompletionRequest, responses bool) (nativeCachePolicy, error) {
	if p.customBaseURL || p.liteLLMChat || p.deepSeekChatDialect {
		return nativeCachePolicy{}, invalidCachePolicy("unsupported route")
	}
	if _, retention := req.Options["prompt_cache_retention"]; retention || p.responsesDefaults.promptCacheRetention != "" {
		return nativeCachePolicy{}, invalidCachePolicy("legacy retention conflict")
	}
	object, ok := cachePolicyObject(raw)
	if !ok {
		return nativeCachePolicy{}, invalidCachePolicy("expected policy object")
	}
	for key := range object {
		if key != "mode" && key != "ttl" && key != "breakpoints" {
			return nativeCachePolicy{}, invalidCachePolicy("unknown policy member")
		}
	}
	mode, ok := object["mode"].(string)
	if !ok {
		return nativeCachePolicy{}, invalidCachePolicy("required canonical mode")
	}
	parsedMode, err := ParseNativeCacheMode(mode)
	if err != nil || parsedMode.String() != mode {
		return nativeCachePolicy{}, invalidCachePolicy("required canonical mode")
	}
	policy := nativeCachePolicy{mode: parsedMode}
	if rawTTL, exists := object["ttl"]; exists {
		policy.ttl, err = parseNativeCacheTTL(rawTTL, responses)
		if err != nil {
			return nativeCachePolicy{}, err
		}
	}
	if rawBreakpoints, exists := object["breakpoints"]; exists {
		selectors, ok := rawBreakpoints.([]any)
		if !ok || selectors == nil {
			return nativeCachePolicy{}, invalidCachePolicy("expected breakpoint array")
		}
		limit := 4
		if policy.mode == NativeCacheModes.CACHEIMPLICIT {
			limit = 3
		}
		if len(selectors) > limit {
			return nativeCachePolicy{}, invalidCachePolicy("breakpoint limit")
		}
		for _, rawSelector := range selectors {
			selector, err := parseNativeCacheSelector(rawSelector, req.Messages, responses)
			if err != nil {
				return nativeCachePolicy{}, err
			}
			for _, prior := range policy.breakpoints {
				if prior.message == selector.message && prior.content == selector.content {
					return nativeCachePolicy{}, invalidCachePolicy("duplicate breakpoint")
				}
			}
			policy.breakpoints = append(policy.breakpoints, selector)
		}
	}
	return policy, nil
}

func parseNativeCacheTTL(raw any, responses bool) (NativeCacheTTL, error) {
	text, ok := raw.(string)
	if ok {
		parsed, err := ParseNativeCacheTTL(text)
		if err == nil && parsed.String() == text && (responses || parsed == NativeCacheTTLs.CACHE30M) {
			return parsed, nil
		}
	}
	return NativeCacheTTL{}, invalidCachePolicy("unsupported canonical ttl")
}

func parseNativeCacheSelector(raw any, messages []llm.Message, responses bool) (nativeCacheSelector, error) {
	object, ok := cachePolicyObject(raw)
	if !ok {
		return nativeCacheSelector{}, invalidCachePolicy("expected breakpoint object")
	}
	for key := range object {
		if key != "message_index" && key != "content_index" && key != "ttl" {
			return nativeCacheSelector{}, invalidCachePolicy("unknown breakpoint member")
		}
	}
	message, ok := nativeCacheIndex(object["message_index"], len(messages))
	if !ok {
		return nativeCacheSelector{}, invalidCachePolicy("invalid message index")
	}
	msg := messages[message]
	bound := 1
	if len(msg.Parts) > 0 {
		bound = len(msg.Parts)
	}
	content, ok := nativeCacheIndex(object["content_index"], bound)
	if !ok {
		return nativeCacheSelector{}, invalidCachePolicy("invalid content index")
	}
	if msg.Role != llm.RoleUser && msg.Role != llm.RoleSystem || msg.Role == llm.RoleSystem && len(msg.Parts) > 0 {
		return nativeCacheSelector{}, invalidCachePolicy("unsupported selected message")
	}
	if len(msg.Parts) == 0 {
		if msg.Content == "" {
			return nativeCacheSelector{}, invalidCachePolicy("empty selected text")
		}
	} else if part := msg.Parts[content]; part.Type != llm.ContentTypeText || part.Text == "" {
		return nativeCacheSelector{}, invalidCachePolicy("selected content is not nonempty text")
	}
	selector := nativeCacheSelector{message: message, content: content}
	if rawTTL, exists := object["ttl"]; exists {
		var err error
		selector.ttl, err = parseNativeCacheTTL(rawTTL, responses)
		if err != nil {
			return nativeCacheSelector{}, err
		}
	}
	return selector, nil
}

func nativeCacheIndex(raw any, bound int) (int, bool) {
	var signed int64
	switch value := raw.(type) {
	case int:
		signed = int64(value)
	case int8:
		signed = int64(value)
	case int16:
		signed = int64(value)
	case int32:
		signed = int64(value)
	case int64:
		signed = value
	case uint:
		return nativeCacheUnsignedIndex(uint64(value), bound)
	case uint8:
		return nativeCacheUnsignedIndex(uint64(value), bound)
	case uint16:
		return nativeCacheUnsignedIndex(uint64(value), bound)
	case uint32:
		return nativeCacheUnsignedIndex(uint64(value), bound)
	case uint64:
		return nativeCacheUnsignedIndex(value, bound)
	case float32:
		return nativeCacheFloatIndex(float64(value), bound)
	case float64:
		return nativeCacheFloatIndex(value, bound)
	case json.Number:
		return nativeCacheNumberIndex(value, bound)
	default:
		return 0, false
	}
	if signed < 0 || signed >= int64(bound) {
		return 0, false
	}
	return int(signed), true
}

func nativeCacheUnsignedIndex(value uint64, bound int) (int, bool) {
	if bound <= 0 || value > math.MaxInt {
		return 0, false
	}
	if value >= uint64(bound) {
		return 0, false
	}
	return int(value), true
}

func nativeCacheFloatIndex(value float64, bound int) (int, bool) {
	if math.IsNaN(value) || value < 0 || value >= float64(bound) || math.Trunc(value) != value {
		return 0, false
	}
	return int(value), true
}

// nativeCacheNumberIndex checks decimal integrality without floating-point
// rounding or allocating a big integer for attacker-selected exponents.
func nativeCacheNumberIndex(value json.Number, bound int) (int, bool) {
	text := value.String()
	if !json.Valid([]byte(text)) {
		return 0, false
	}
	mantissa, exponent, _ := strings.Cut(strings.ToLower(text), "e")
	whole, fraction, _ := strings.Cut(mantissa, ".")
	digits := strings.TrimLeft(strings.TrimPrefix(whole, "-")+fraction, "0")
	if digits == "" {
		return 0, bound > 0
	}
	if strings.HasPrefix(mantissa, "-") {
		return 0, false
	}
	var exp int64
	if exponent != "" {
		var err error
		exp, err = strconv.ParseInt(exponent, 10, 64)
		if err != nil {
			return 0, false
		}
	}
	trimmed := strings.TrimRight(digits, "0")
	scale := int64(len(fraction) - (len(digits) - len(trimmed)))
	if exp < scale || exp > scale+19 || int64(len(trimmed)) > 19-(exp-scale) {
		return 0, false
	}
	integer, err := strconv.ParseUint(trimmed+strings.Repeat("0", int(exp-scale)), 10, 64)
	if err != nil {
		return 0, false
	}
	return nativeCacheUnsignedIndex(integer, bound)
}

func (p *nativeCachePolicy) selected(message, content int) (nativeCacheSelector, bool) {
	if p != nil {
		for _, selector := range p.breakpoints {
			if selector.message == message && selector.content == content {
				return selector, true
			}
		}
	}
	return nativeCacheSelector{}, false
}

func nativeCacheTTLString(ttl NativeCacheTTL) string {
	if ttl.IsValid() {
		return ttl.String()
	}
	return ""
}
