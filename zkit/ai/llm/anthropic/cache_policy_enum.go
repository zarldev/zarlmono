package anthropic

//go:generate go tool goenums -f cache_policy_enum.go

// cacheStrategy selects provider-generated request breakpoints.
type cacheStrategy int

const (
	threeRegion cacheStrategy = iota // three_region
	nativeOnly                       // native_only
)

// cacheTTL is an explicit prompt-cache lifetime. Its natural zero is five minutes.
type cacheTTL int

const (
	cache5m cacheTTL = iota // 5m
	cache1h                 // 1h
)
