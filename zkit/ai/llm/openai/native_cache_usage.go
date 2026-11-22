package openai

import (
	"bytes"
	"encoding/json"
	"time"

	sdk "github.com/openai/openai-go/v3"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func (p *Provider) nativeOpenAIUsage() bool {
	return !p.customBaseURL && !p.liteLLMChat && !p.deepSeekChatDialect
}

// nativeChatUsage preserves the dialect mapper and adds only the documented
// native aggregate. Chat lifetime semantics are not verified.
func (p *Provider) nativeChatUsage(u sdk.CompletionUsage) llm.Usage {
	usage := p.chatUsage(u)
	if p.nativeOpenAIUsage() {
		write := nativeChatCacheWrite(u)
		if write.Known {
			usage.CacheCreationTokens = int(write.Tokens)
		}
	}
	return usage
}

// Native cache evidence uses only exact documented wire keys. In particular,
// encoding/json's case-insensitive struct-field matching must not add aliases.
func nativeCacheObject(raw []byte) map[string]json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	return fields
}

func nativeChatCacheWrite(u sdk.CompletionUsage) llm.TokenQuantity {
	raw := nativeCacheObject([]byte(u.RawJSON()))
	details := nativeCacheObject(raw["prompt_tokens_details"])
	write, _ := nativeBoundedCacheWrite(nativeTokenQuantity(raw["prompt_tokens"]), nativeTokenQuantity(details["cached_tokens"]), nativeTokenQuantity(details["cache_write_tokens"]))
	return write
}

func nativeNonNull(raw json.RawMessage) bool {
	return len(raw) != 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func nativeTokenQuantity(raw json.RawMessage) llm.TokenQuantity {
	var count int
	if !nativeNonNull(raw) || json.Unmarshal(raw, &count) != nil || count < 0 {
		return llm.TokenQuantity{}
	}
	return llm.TokenQuantity{Tokens: int64(count), Known: true}
}

// The boolean marks a subset contradiction, which also invalidates lifetime
// evidence. Subtraction avoids overflowing when two individually valid counts
// cannot both fit the inclusive input total.
func nativeBoundedCacheWrite(total, read, write llm.TokenQuantity) (llm.TokenQuantity, bool) {
	if total.Known {
		if read.Known && read.Tokens > total.Tokens {
			return llm.TokenQuantity{}, true
		}
		limit := total.Tokens
		if read.Known {
			limit -= read.Tokens
		}
		if write.Known && write.Tokens > limit {
			return llm.TokenQuantity{}, true
		}
	}
	return write, false
}

// nativeResponsesCacheUsage projects only explicitly observed cache-write
// evidence from https://developers.openai.com/api/docs/guides/prompt-caching.md.
// It deliberately does not claim full modality or billing coverage.
func nativeResponsesCacheUsage(rawJSON []byte) *llm.PricingUsage {
	raw := nativeCacheObject(rawJSON)
	details := nativeCacheObject(raw["input_tokens_details"])
	if !nativeNonNull(details["cache_write_tokens"]) && !nativeNonNull(details["cache_write_tokens_details"]) {
		return nil
	}
	usage := llm.PricingUsage{
		Input: llm.PricingInputUsage{
			Total:     nativeTokenQuantity(raw["input_tokens"]),
			CacheRead: nativeTokenQuantity(details["cached_tokens"]),
		},
		Output:                    llm.PricingOutputUsage{Total: nativeTokenQuantity(raw["output_tokens"])},
		AdditionalChargesPossible: true,
	}
	var contradiction bool
	usage.Input.CacheWrite, contradiction = nativeBoundedCacheWrite(usage.Input.Total, usage.Input.CacheRead, nativeTokenQuantity(details["cache_write_tokens"]))
	if usage.Input.Total.Known && usage.Input.CacheRead.Known && usage.Input.CacheRead.Tokens > usage.Input.Total.Tokens {
		usage.Input.CacheRead = llm.TokenQuantity{}
	}
	if nativeNonNull(details["cache_write_tokens_details"]) {
		// New malformed optional fields are unknown evidence, not new transport
		// errors. The legacy typed usage decode remains authoritative for errors.
		lifetimes := nativeCacheObject(details["cache_write_tokens_details"])
		short, long := nativeTokenQuantity(lifetimes["ttl_30m_tokens"]), nativeTokenQuantity(lifetimes["ttl_12h_tokens"])
		if write := usage.Input.CacheWrite; write.Known {
			if (short.Known && short.Tokens > write.Tokens) || (long.Known && long.Tokens > write.Tokens) {
				contradiction = true
			} else if short.Known && long.Known && short.Tokens != write.Tokens-long.Tokens {
				contradiction = true
			}
		}
		if contradiction {
			short, long = llm.TokenQuantity{}, llm.TokenQuantity{}
		}
		usage.Input.WritesByLifetime = []llm.CacheWriteQuantity{
			{Lifetime: 30 * time.Minute, Quantity: short},
			{Lifetime: 12 * time.Hour, Quantity: long},
		}
	}
	return &usage
}
