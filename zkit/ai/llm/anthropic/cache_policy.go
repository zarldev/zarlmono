package anthropic

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/options"
)

const (
	// CacheStrategyOption accepts the canonical string "three_region" or
	// "native_only" in CompletionRequest.Options.
	CacheStrategyOption = "anthropic_cache_strategy"
	// CacheStaticTTLOption accepts "5m" or "1h" for new tools/system markers.
	CacheStaticTTLOption = "anthropic_cache_static_ttl"
	// CacheRollingTTLOption accepts "5m" or "1h" for a new message marker.
	CacheRollingTTLOption = "anthropic_cache_rolling_ttl"
)

// ErrInvalidCachePolicy identifies invalid request cache options, native
// breakpoints, or a proposed sequence exceeding the slot/TTL-order constraints.
// Diagnostics contain structural paths and fixed reasons, never payload values.
var ErrInvalidCachePolicy = errors.New("anthropic: invalid cache policy")

const (
	cacheTypeField    = "type"
	cacheTextField    = "text"
	cacheControlField = "cache_control"
)

type cachePolicy struct {
	strategy CacheStrategy
	static   CacheTTL
	rolling  CacheTTL
}

// WithCacheStrategy selects provider-generated markers. Native-only preserves
// and validates native markers; it does not promise to disable server caching.
func WithCacheStrategy(strategy CacheStrategy) options.Option[Provider] {
	return func(p *Provider) { p.cachePolicy.strategy = strategy }
}

// WithCacheTTLs sets lifetimes for new static and rolling markers respectively.
// Existing native lifetimes always win at a selected site.
func WithCacheTTLs(static, rolling CacheTTL) options.Option[Provider] {
	return func(p *Provider) {
		p.cachePolicy.static = static
		p.cachePolicy.rolling = rolling
	}
}

func (p *Provider) requestCachePolicy(modelOptions llm.ModelOptions) (cachePolicy, error) {
	policy := p.cachePolicy
	if value, exists := modelOptions[CacheStrategyOption]; exists {
		text, ok := value.(string)
		strategy, err := ParseCacheStrategy(text)
		if !ok || err != nil || strategy.String() != text {
			return cachePolicy{}, invalidCachePolicy(CacheStrategyOption, "expected canonical strategy string")
		}
		policy.strategy = strategy
	}
	var err error
	policy.static, err = requestCacheTTL(modelOptions, CacheStaticTTLOption, policy.static)
	if err != nil {
		return cachePolicy{}, err
	}
	policy.rolling, err = requestCacheTTL(modelOptions, CacheRollingTTLOption, policy.rolling)
	if err != nil {
		return cachePolicy{}, err
	}
	return policy, nil
}

func requestCacheTTL(modelOptions llm.ModelOptions, key string, fallback CacheTTL) (CacheTTL, error) {
	value, exists := modelOptions[key]
	if !exists {
		return fallback, nil
	}
	text, ok := value.(string)
	ttl, err := ParseCacheTTL(text)
	if !ok || err != nil || ttl.String() != text {
		return CacheTTL{}, invalidCachePolicy(key, "expected canonical TTL string")
	}
	return ttl, nil
}

func invalidCachePolicy(path, reason string) error {
	return fmt.Errorf("%w: %s: %s", ErrInvalidCachePolicy, path, reason)
}

// cacheSite describes a direct SDK wire object, not arbitrary nested values.
// Exactly one of tool/system/message is nonnegative. All bytes are owned by
// this request; the SDK marshaler honors raw param.Override values here.
type cacheSite struct {
	tool, system, message, block int
	path                         string
	raw                          []byte
	eligible                     bool
	marked                       bool
	ttl                          CacheTTL
	add                          bool
}

func (p *Provider) applyCachePolicy(params *anthropic.MessageNewParams, modelOptions llm.ModelOptions) error {
	policy, err := p.requestCachePolicy(modelOptions)
	if err != nil {
		return err
	}
	sites, err := requestCacheSites(params)
	if err != nil {
		return err
	}
	if policy.strategy == CacheStrategies.THREEREGION {
		selectCacheSites(sites, policy)
	}
	if err := validateCacheSequence(sites); err != nil {
		return err
	}
	for _, site := range sites {
		if !site.add {
			continue
		}
		// Insert only our new member. Unknown native fields and their values,
		// including nested opaque objects, retain their original JSON bytes.
		raw := bytes.TrimSpace(site.raw)
		control := `,"cache_control":{"type":"ephemeral"}`
		if site.ttl == CacheTTLs.CACHE1H {
			control = `,"cache_control":{"type":"ephemeral","ttl":"1h"}`
		}
		added := make([]byte, 0, len(raw)+len(control))
		added = append(added, raw[:len(raw)-1]...)
		added = append(added, control...)
		added = append(added, '}')
		switch {
		case site.tool >= 0:
			params.Tools[site.tool] = param.Override[anthropic.ToolUnionParam](json.RawMessage(added))
		case site.system >= 0:
			params.System[site.system] = param.Override[anthropic.TextBlockParam](json.RawMessage(added))
		default:
			params.Messages[site.message].Content[site.block] = param.Override[anthropic.ContentBlockParamUnion](json.RawMessage(added))
		}
	}
	return nil
}

func requestCacheSites(params *anthropic.MessageNewParams) ([]cacheSite, error) {
	var sites []cacheSite
	appendSite := func(value any, site cacheSite) error {
		raw, err := json.Marshal(value)
		if err != nil {
			return invalidCachePolicy(site.path, "invalid wire object")
		}
		site.raw = raw
		if err := inspectCacheSite(&site); err != nil {
			return err
		}
		sites = append(sites, site)
		return nil
	}
	for i, tool := range params.Tools {
		if err := appendSite(tool, cacheSite{tool: i, system: -1, message: -1, path: fmt.Sprintf("tools[%d]", i)}); err != nil {
			return nil, err
		}
	}
	for i, system := range params.System {
		if err := appendSite(system, cacheSite{tool: -1, system: i, message: -1, path: fmt.Sprintf("system[%d]", i)}); err != nil {
			return nil, err
		}
	}
	for i, message := range params.Messages {
		for j, block := range message.Content {
			if err := appendSite(block, cacheSite{tool: -1, system: -1, message: i, block: j, path: fmt.Sprintf("messages[%d].content[%d]", i, j)}); err != nil {
				return nil, err
			}
		}
	}
	return sites, nil
}

func inspectCacheSite(site *cacheSite) error {
	fields, err := cacheObjectFields(site.raw, site.path, false)
	if err != nil {
		return err
	}
	site.eligible = site.tool >= 0
	if site.tool < 0 {
		var kind string
		if json.Unmarshal(fields[cacheTypeField], &kind) == nil {
			switch kind {
			case continuationText:
				var text string
				site.eligible = json.Unmarshal(fields[cacheTextField], &text) == nil && text != ""
			case "image", "tool_use", "tool_result":
				site.eligible = site.message >= 0
			}
		}
	}
	control, exists := fields[cacheControlField]
	if !exists {
		return nil
	}
	if !site.eligible {
		return invalidCachePolicy(site.path, "marker on ineligible block")
	}
	ttl, err := nativeCacheTTL(control, site.path+".cache_control")
	if err != nil {
		return err
	}
	site.marked, site.ttl = true, ttl
	return nil
}

// cacheObjectFields rejects duplicate structural keys and noncanonical aliases
// that encoding/json would match case-insensitively when decoding Go structs.
// Nested schemas, citations, tool inputs/results and other opaque data stay opaque.
func cacheObjectFields(raw []byte, path string, control bool) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, invalidCachePolicy(path, "expected object")
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, invalidCachePolicy(path, "invalid object")
		}
		key, ok := token.(string)
		if !ok {
			return nil, invalidCachePolicy(path, "invalid object key")
		}
		for _, structural := range []string{cacheTypeField, cacheTextField, cacheControlField} {
			if key != structural && strings.EqualFold(key, structural) {
				return nil, invalidCachePolicy(path, "noncanonical structural key")
			}
		}
		if _, exists := fields[key]; exists && (control || key == cacheControlField || key == cacheTypeField || key == cacheTextField) {
			return nil, invalidCachePolicy(path, "duplicate structural key")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, invalidCachePolicy(path, "invalid object value")
		}
		fields[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, invalidCachePolicy(path, "invalid object end")
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, invalidCachePolicy(path, "trailing object data")
	}
	return fields, nil
}

func nativeCacheTTL(raw []byte, path string) (CacheTTL, error) {
	fields, err := cacheObjectFields(raw, path, true)
	if err != nil {
		return CacheTTL{}, err
	}
	var kind string
	if json.Unmarshal(fields[cacheTypeField], &kind) != nil || kind != "ephemeral" {
		return CacheTTL{}, invalidCachePolicy(path, "expected ephemeral type")
	}
	for key := range fields {
		if key != cacheTypeField && key != "ttl" {
			return CacheTTL{}, invalidCachePolicy(path, "unsupported control field")
		}
	}
	ttl := CacheTTLs.CACHE5M
	if value, exists := fields["ttl"]; exists {
		var text string
		if json.Unmarshal(value, &text) != nil || (text != "5m" && text != "1h") {
			return CacheTTL{}, invalidCachePolicy(path, "expected canonical TTL string")
		}
		ttl, _ = ParseCacheTTL(text) // Canonical boundary check above establishes validity.
	}
	return ttl, nil
}

func selectCacheSites(sites []cacheSite, policy cachePolicy) {
	tool, system, rolling := -1, -1, -1
	for i, site := range sites {
		switch {
		case site.tool >= 0:
			tool = i
		case site.system >= 0:
			system = i
		case site.eligible:
			rolling = i
		}
	}
	for _, index := range []int{tool, system, rolling} {
		if index < 0 || sites[index].marked {
			continue
		}
		sites[index].add = true
		sites[index].ttl = policy.static
		if index == rolling {
			sites[index].ttl = policy.rolling
		}
	}
}

func validateCacheSequence(sites []cacheSite) error {
	count := 0
	shortSeen := false
	for _, site := range sites {
		if !site.marked && !site.add {
			continue
		}
		count++
		if count > 4 {
			return invalidCachePolicy(site.path, "more than four breakpoints")
		}
		if site.ttl == CacheTTLs.CACHE1H && shortSeen {
			return invalidCachePolicy(site.path, "one-hour breakpoint follows five-minute breakpoint")
		}
		shortSeen = shortSeen || site.ttl == CacheTTLs.CACHE5M
	}
	return nil
}
