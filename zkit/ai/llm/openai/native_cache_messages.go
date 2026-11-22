package openai

import (
	"bytes"
	"encoding/json"
	"iter"
	"strings"

	sdk "github.com/openai/openai-go/v3"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// nativeCacheMessageSources retains original source indices while delegating
// each complete tool-result run to the canonical attachment projector. Tool
// results and transport-only attachment messages cannot be selected.
func nativeCacheMessageSources(messages []llm.Message) iter.Seq2[int, llm.Message] {
	return func(yield func(int, llm.Message) bool) {
		for index := 0; index < len(messages); {
			if messages[index].Role != llm.RoleTool {
				if !yield(index, messages[index]) {
					return
				}
				index++
				continue
			}
			end := index + 1
			for end < len(messages) && messages[end].Role == llm.RoleTool {
				end++
			}
			for _, projected := range llm.ExpandToolResultParts(messages[index:end]) {
				if !yield(-1, projected) {
					return
				}
			}
			index = end
		}
	}
}

func nativeCacheChatText(text string, selector nativeCacheSelector) sdk.ChatCompletionContentPartTextParam {
	marker := sdk.NewChatCompletionContentPartTextPromptCacheBreakpointParam()
	marker.Ttl = nativeCacheTTLString(selector.ttl)
	return sdk.ChatCompletionContentPartTextParam{Text: text, PromptCacheBreakpoint: marker}
}

func nativeCacheChatOptions(policy *nativeCachePolicy) sdk.ChatCompletionNewParamsPromptCacheOptions {
	if policy == nil {
		return sdk.ChatCompletionNewParamsPromptCacheOptions{}
	}
	return sdk.ChatCompletionNewParamsPromptCacheOptions{Mode: policy.mode.String(), Ttl: nativeCacheTTLString(policy.ttl)}
}

type nativeCacheOptionsWire struct {
	Mode string `json:"mode"`
	TTL  string `json:"ttl,omitempty"`
}

type nativeCacheBreakpointWire struct {
	Mode string `json:"mode"`
	TTL  string `json:"ttl,omitempty"`
}

func nativeCacheResponsesOptions(policy *nativeCachePolicy) *nativeCacheOptionsWire {
	if policy == nil {
		return nil
	}
	return &nativeCacheOptionsWire{Mode: policy.mode.String(), TTL: nativeCacheTTLString(policy.ttl)}
}

func nativeCacheResponsesMarker(policy *nativeCachePolicy, message, content int) *nativeCacheBreakpointWire {
	selector, ok := policy.selected(message, content)
	if !ok {
		return nil
	}
	return &nativeCacheBreakpointWire{Mode: NativeCacheModes.CACHEEXPLICIT.String(), TTL: nativeCacheTTLString(selector.ttl)}
}

// nativeCacheReplayMarker inspects only direct content blocks of an item that
// the Responses converter actually consumes. Decode each content spelling (and
// duplicate) independently so JSON's last-member and case-folding rules cannot
// hide a reserved marker. Nested schemas, annotations and encrypted data remain
// opaque, and no replay data is rewritten.
func nativeCacheReplayMarker(raw json.RawMessage) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return false
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return false
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return false
		}
		name, ok := key.(string)
		if !ok || !strings.EqualFold(name, "content") {
			continue
		}
		var blocks []json.RawMessage
		if json.Unmarshal(value, &blocks) != nil {
			continue
		}
		for _, rawBlock := range blocks {
			var block map[string]json.RawMessage
			if json.Unmarshal(rawBlock, &block) != nil {
				continue
			}
			for member := range block {
				if strings.EqualFold(member, "prompt_cache_breakpoint") {
					return true
				}
			}
		}
	}
	return false
}
