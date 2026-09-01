package cache

import (
	"context"
	"errors"
)

var (
	// ErrNotFound reports a Get for a key the cache doesn't hold (or one
	// whose entry expired). Callers treat it as "compute and Set", not as
	// a failure.
	ErrNotFound = errors.New("key not found")
)

// Reader defines basic read operations for cache implementations.
type Reader[K comparable, V any] interface {
	// Get retrieves the value associated with the given key.
	// Returns ErrNotFound if the key does not exist.
	// Returns ctx.Err() when the context is canceled or its deadline expires.
	Get(ctx context.Context, key K) (V, error)

	// Len returns the number of entries in the cache.
	// Returns ctx.Err() when the context is canceled or its deadline expires.
	Len(ctx context.Context) (int, error)
}

// Writer defines basic write operations for cache implementations.
type Writer[K comparable, V any] interface {
	// Set stores a key-value pair in the cache.
	// If the key already exists, its value is updated.
	// Returns ctx.Err() when the context is canceled or its deadline expires.
	Set(ctx context.Context, key K, value V) error

	// Delete removes a key-value pair from the cache.
	// Returns true if the key existed and was deleted, false otherwise.
	// Returns ctx.Err() when the context is canceled or its deadline expires.
	Delete(ctx context.Context, key K) (bool, error)

	// Clear removes all entries from the cache.
	// Returns ctx.Err() when the context is canceled or its deadline expires.
	Clear(ctx context.Context) error
}

// ReadWriter combines basic read and write operations.
type ReadWriter[K comparable, V any] interface {
	Reader[K, V]
	Writer[K, V]
}

// Cache combines all common cache operations.
type Cache[K comparable, V any] interface {
	ReadWriter[K, V]
	Healthy(ctx context.Context) error
}
