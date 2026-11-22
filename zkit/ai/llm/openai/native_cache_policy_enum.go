package openai

//go:generate go tool goenums -f native_cache_policy_enum.go

// nativeCacheMode controls implicit breakpoint creation. Zero is invalid.
type nativeCacheMode int

const (
	cacheImplicit nativeCacheMode = iota + 1 // implicit
	cacheExplicit                            // explicit
)

// nativeCacheTTL is a minimum cache lifetime. Zero means no explicit TTL in
// resolved policy values, and is not a valid member of this domain.
type nativeCacheTTL int

const (
	cache30m nativeCacheTTL = iota + 1 // 30m
	cache12h                           // 12h
)
