package options

// Option is a generic functional option that modifies a struct of type T.
// It follows the standard Go functional options pattern where options
// are functions that modify the target struct in place.
type Option[T any] func(*T)
