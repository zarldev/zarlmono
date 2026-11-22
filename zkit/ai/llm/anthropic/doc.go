// Package anthropic adapts Anthropic Claude models to the shared LLM provider
// interface.
//
// It wraps the official Anthropic SDK without retaining conversation state.
// Each lazy completion consumes the supplied provider-neutral request, applies
// model-aware request policy, and preserves captured native continuation blocks.
// Streaming and nonstreaming calls share request shaping and usage semantics.
//
// Prompt caching defaults to three explicit breakpoints: the last tool and
// system block (one hour), and the nearest eligible message block searched
// backward across the entire history (five minutes). WithCacheStrategy and
// WithCacheTTLs set constructor policy; canonical string request options
// CacheStrategyOption, CacheStaticTTLOption and CacheRollingTTLOption override
// each field independently. Options are parsed only when iteration begins.
// Native-only adds no markers, but preserves and validates native ones; it is
// not a server-cache disable switch. Native lifetimes at selected sites win.
//
// Native and generated markers together must fit four slots, with all one-hour
// markers preceding five-minute markers in tools, system, then message wire
// order. Invalid policies fail before dispatch with ErrInvalidCachePolicy;
// markers are never silently removed or their lifetimes rewritten. Placement
// does not track historical writes or guarantee cache hits. The service's
// 20-position lookback includes the breakpoint; long histories can move old
// writes outside that window. No bridge markers or automatic caching are added.
package anthropic
