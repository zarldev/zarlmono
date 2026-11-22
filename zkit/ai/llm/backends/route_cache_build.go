package backends

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/anthropic"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

func (r *ProviderRegistry) resolveConstructionCache(ctx context.Context, def ProviderDefinition, alias string, intent RouteCachePolicy) (RouteCachePolicy, error) {
	if err := ValidateRouteCachePolicy(intent); err != nil {
		return RouteCachePolicy{}, err
	}
	if intent.Mode != RouteCacheModes.CACHEINHERIT {
		return intent, nil
	}
	fallback := RouteCachePolicy{Mode: RouteCacheModes.CACHEDEFAULT}
	if r.routeCachePolicies == nil || (def.AdapterType != AdapterTypes.LITELLM && def.AdapterType != AdapterTypes.ANTHROPICCOMPATIBLE) || alias == "" {
		return fallback, nil
	}
	if err := validateConstructionCacheKey(def.Name, alias); err != nil {
		return RouteCachePolicy{}, err
	}
	saved, err := r.routeCachePolicies.GetRouteCachePolicy(ctx, def.Name, alias)
	switch {
	case err == nil:
		if err := validateStoredRouteCachePolicy(saved); err != nil {
			return RouteCachePolicy{}, err
		}
		return saved, nil
	case errors.Is(err, ErrRouteCachePolicyNotFound):
		return fallback, nil
	default:
		return RouteCachePolicy{}, fmt.Errorf("get route cache policy: %w", err)
	}
}

func validateConstructionCacheKey(provider, alias string) error {
	if provider == "" || alias == "" || !utf8.ValidString(provider) || !utf8.ValidString(alias) {
		return fmt.Errorf("%w: keys require nonempty UTF-8", ErrInvalidRouteCachePolicy)
	}
	return nil
}

func validateConstructionCache(at AdapterType, protocol llm.ModelProtocol, p RouteCachePolicy) error {
	if p.Mode == RouteCacheModes.CACHEDEFAULT {
		return nil
	}
	if at == AdapterTypes.ANTHROPICCOMPATIBLE || (at == AdapterTypes.LITELLM && protocol == llm.ModelProtocols.PROTOCOLMESSAGES) {
		if p.Mode == RouteCacheModes.CACHEEXPLICIT {
			if p.System || p.Tools {
				return fmt.Errorf("%w: Messages selects its own marker sites", ErrInvalidRouteCachePolicy)
			}
			if p.StaticTTL == RouteCacheTTLs.CACHETTL5M && p.RollingTTL == RouteCacheTTLs.CACHETTL1H {
				return fmt.Errorf("%w: one-hour rolling marker follows five-minute static marker", ErrInvalidRouteCachePolicy)
			}
		}
		return nil
	}
	if at == AdapterTypes.LITELLM && protocol == llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS {
		if p.Mode == RouteCacheModes.CACHEEXPLICIT && (!p.System && !p.Tools || p.RollingTTL != RouteCacheTTLs.CACHETTLDEFAULT) {
			return fmt.Errorf("%w: Chat requires a selected static site and no rolling lifetime", ErrInvalidRouteCachePolicy)
		}
		return nil
	}
	return fmt.Errorf("%w: route does not support cache marker policy", ErrInvalidRouteCachePolicy)
}

// effectiveConstructionCache resolves selectors to the marker policy actually
// installed on this construction. Other adapters retain their no-override default.
func effectiveConstructionCache(at AdapterType, protocol llm.ModelProtocol, p RouteCachePolicy) RouteCachePolicy {
	if at == AdapterTypes.ANTHROPICCOMPATIBLE || (at == AdapterTypes.LITELLM && protocol == llm.ModelProtocols.PROTOCOLMESSAGES) {
		if p.Mode == RouteCacheModes.CACHEDEFAULT {
			p.Mode = RouteCacheModes.CACHEEXPLICIT
		}
		if p.Mode == RouteCacheModes.CACHEEXPLICIT {
			if p.StaticTTL == RouteCacheTTLs.CACHETTLDEFAULT {
				p.StaticTTL = RouteCacheTTLs.CACHETTL1H
			}
			if p.RollingTTL == RouteCacheTTLs.CACHETTLDEFAULT {
				p.RollingTTL = RouteCacheTTLs.CACHETTL5M
			}
		}
	} else if at == AdapterTypes.LITELLM && protocol == llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS {
		if p.Mode == RouteCacheModes.CACHEDEFAULT {
			p.Mode = RouteCacheModes.CACHEDISABLED
		}
		if p.Mode == RouteCacheModes.CACHEEXPLICIT && p.StaticTTL == RouteCacheTTLs.CACHETTLDEFAULT {
			p.StaticTTL = RouteCacheTTLs.CACHETTL5M
		}
	}
	return p
}

func messagesCachePolicy(p RouteCachePolicy) (anthropic.CacheStrategy, anthropic.CacheTTL, anthropic.CacheTTL) {
	strategy, static, rolling := anthropic.CacheStrategies.THREEREGION, anthropic.CacheTTLs.CACHE1H, anthropic.CacheTTLs.CACHE5M
	if p.Mode == RouteCacheModes.CACHEDISABLED {
		strategy = anthropic.CacheStrategies.NATIVEONLY
	}
	if p.StaticTTL == RouteCacheTTLs.CACHETTL5M {
		static = anthropic.CacheTTLs.CACHE5M
	}
	if p.RollingTTL == RouteCacheTTLs.CACHETTL1H {
		rolling = anthropic.CacheTTLs.CACHE1H
	}
	return strategy, static, rolling
}

func chatCachePolicy(p RouteCachePolicy) openai.LiteLLMPromptCachePolicy {
	if p.Mode != RouteCacheModes.CACHEEXPLICIT {
		return openai.LiteLLMPromptCachePolicy{}
	}
	policy := openai.LiteLLMPromptCachePolicy{System: p.System, Tools: p.Tools}
	switch p.StaticTTL {
	case RouteCacheTTLs.CACHETTL5M:
		policy.TTL = openai.LiteLLMCacheTTLs.LITELLMCACHE5M
	case RouteCacheTTLs.CACHETTL1H:
		policy.TTL = openai.LiteLLMCacheTTLs.LITELLMCACHE1H
	}
	return policy
}
