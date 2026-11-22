package backends

//go:generate go tool goenums -f route_cache_policy_enum.go

// RouteCacheMode selects whether an alias inherits, masks, disables generated markers,
// or explicitly requests cache markers. Zero is runtime inheritance, not a stored row.
type routeCacheMode int

const (
	cacheInherit  routeCacheMode = iota // inherit
	cacheDefault                        // default
	cacheDisabled                       // disabled
	cacheExplicit                       // explicit
)

// RouteCacheTTL is a requested minimum lifetime. Zero uses the adapter default.
type routeCacheTTL int

const (
	cacheTTLDefault routeCacheTTL = iota // default
	cacheTTL5m                           // 5m
	cacheTTL1h                           // 1h
)
