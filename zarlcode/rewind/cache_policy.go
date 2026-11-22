package rewind

import (
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

const cacheDefault = "default"

// CachePolicy is the closed, comparable checkpoint representation of a cache
// policy. Strings prevent generated enums from accepting numeric JSON values.
// Its zero value means absent, preserving legacy version-one JSON bytes.
type CachePolicy struct {
	Mode       string `json:"mode"`
	StaticTTL  string `json:"static_ttl"`
	RollingTTL string `json:"rolling_ttl"`
	System     bool   `json:"system"`
	Tools      bool   `json:"tools"`
}

// UnmarshalJSON accepts complete, canonical policy objects. Absence is encoded
// by omitting the Target field, never by an explicitly empty policy object.
func (p *CachePolicy) UnmarshalJSON(data []byte) error {
	type wire CachePolicy
	var decoded wire
	if !strictJSON(data, &decoded) || CachePolicy(decoded) == (CachePolicy{}) {
		return ErrInvalid
	}
	value := CachePolicy(decoded)
	if _, err := value.RoutePolicy(); err != nil {
		return err
	}
	*p = value
	return nil
}

// SavedCachePolicy converts a construction-resolved value to closed transport.
// Exact factory defaults are omitted so legacy checkpoints retain their bytes.
func SavedCachePolicy(provider string, policy backends.RouteCachePolicy) CachePolicy {
	if provider != providerAnthropic && provider != "litellm" {
		return CachePolicy{}
	}
	if provider == providerAnthropic && policy == (backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEEXPLICIT, StaticTTL: backends.RouteCacheTTLs.CACHETTL1H, RollingTTL: backends.RouteCacheTTLs.CACHETTL5M}) {
		return CachePolicy{}
	}
	return CachePolicy{Mode: policy.Mode.String(), StaticTTL: policy.StaticTTL.String(), RollingTTL: policy.RollingTTL.String(), System: policy.System, Tools: policy.Tools}
}

// SavedCacheIntent records non-inherited runtime intent independently of the pin.
func SavedCacheIntent(policy backends.RouteCachePolicy) CachePolicy {
	if policy.Mode == backends.RouteCacheModes.CACHEINHERIT {
		return CachePolicy{}
	}
	return CachePolicy{Mode: policy.Mode.String(), StaticTTL: policy.StaticTTL.String(), RollingTTL: policy.RollingTTL.String(), System: policy.System, Tools: policy.Tools}
}

// RoutePolicy admits only canonical enum spellings and the valid value shape.
func (p CachePolicy) RoutePolicy() (backends.RouteCachePolicy, error) {
	if p == (CachePolicy{}) {
		return backends.RouteCachePolicy{}, nil
	}
	var policy backends.RouteCachePolicy
	switch p.Mode {
	case cacheDefault:
		policy.Mode = backends.RouteCacheModes.CACHEDEFAULT
	case "disabled":
		policy.Mode = backends.RouteCacheModes.CACHEDISABLED
	case "explicit":
		policy.Mode = backends.RouteCacheModes.CACHEEXPLICIT
	default:
		return policy, ErrInvalid
	}
	switch p.StaticTTL {
	case cacheDefault:
		policy.StaticTTL = backends.RouteCacheTTLs.CACHETTLDEFAULT
	case "5m":
		policy.StaticTTL = backends.RouteCacheTTLs.CACHETTL5M
	case "1h":
		policy.StaticTTL = backends.RouteCacheTTLs.CACHETTL1H
	default:
		return policy, ErrInvalid
	}
	switch p.RollingTTL {
	case cacheDefault:
		policy.RollingTTL = backends.RouteCacheTTLs.CACHETTLDEFAULT
	case "5m":
		policy.RollingTTL = backends.RouteCacheTTLs.CACHETTL5M
	case "1h":
		policy.RollingTTL = backends.RouteCacheTTLs.CACHETTL1H
	default:
		return policy, ErrInvalid
	}
	policy.System, policy.Tools = p.System, p.Tools
	if backends.ValidateRouteCachePolicy(policy) != nil {
		return policy, ErrInvalid
	}
	return policy, nil
}

func validTargetCache(target Target) bool {
	pin, err := target.CachePin.RoutePolicy()
	if err != nil {
		return false
	}
	intent, err := target.CacheIntent.RoutePolicy()
	if err != nil {
		return false
	}
	if target.Provider != providerAnthropic {
		return pin == (backends.RouteCachePolicy{}) && (intent == (backends.RouteCachePolicy{}) || intent.Mode == backends.RouteCacheModes.CACHEDEFAULT)
	}
	if pin != (backends.RouteCachePolicy{}) {
		if pin.Mode != backends.RouteCacheModes.CACHEEXPLICIT && pin.Mode != backends.RouteCacheModes.CACHEDISABLED {
			return false
		}
		if pin.System || pin.Tools || pin.StaticTTL == backends.RouteCacheTTLs.CACHETTL5M && pin.RollingTTL == backends.RouteCacheTTLs.CACHETTL1H {
			return false
		}
		if pin.Mode == backends.RouteCacheModes.CACHEEXPLICIT && (pin.StaticTTL == backends.RouteCacheTTLs.CACHETTLDEFAULT || pin.RollingTTL == backends.RouteCacheTTLs.CACHETTLDEFAULT) {
			return false
		}
		if pin == (backends.RouteCachePolicy{Mode: backends.RouteCacheModes.CACHEEXPLICIT, StaticTTL: backends.RouteCacheTTLs.CACHETTL1H, RollingTTL: backends.RouteCacheTTLs.CACHETTL5M}) {
			return false
		}
	}
	if intent.System || intent.Tools || intent.StaticTTL == backends.RouteCacheTTLs.CACHETTL5M && intent.RollingTTL == backends.RouteCacheTTLs.CACHETTL1H {
		return false
	}
	if intent.Mode == backends.RouteCacheModes.CACHEDEFAULT && pin != (backends.RouteCachePolicy{}) {
		return false
	}
	if intent.Mode == backends.RouteCacheModes.CACHEDISABLED && pin != intent {
		return false
	}
	if intent.Mode == backends.RouteCacheModes.CACHEEXPLICIT {
		expected := intent
		if expected.StaticTTL == backends.RouteCacheTTLs.CACHETTLDEFAULT {
			expected.StaticTTL = backends.RouteCacheTTLs.CACHETTL1H
		}
		if expected.RollingTTL == backends.RouteCacheTTLs.CACHETTLDEFAULT {
			expected.RollingTTL = backends.RouteCacheTTLs.CACHETTL5M
		}
		if SavedCachePolicy(target.Provider, expected) != target.CachePin {
			return false
		}
	}
	return true
}
