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
	// ErrCanceled reports an operation abandoned because the caller's
	// context ended before the backend answered.
	ErrCanceled = errors.New("canceled")
)

// Reader defines basic read operations for cache implementations.
type Reader[K comparable, V any] interface {
	// Get retrieves the value associated with the given key.
	// Returns ErrNotFound if the key does not exist.
	// Returns context.Canceled if the context is canceled.
	Get(ctx context.Context, key K) (V, error)

	// Len returns the number of entries in the cache.
	// Returns context.Canceled if the context is canceled.
	Len(ctx context.Context) (int, error)
}

// Writer defines basic write operations for cache implementations.
type Writer[K comparable, V any] interface {
	// Set stores a key-value pair in the cache.
	// If the key already exists, its value is updated.
	// Returns context.Canceled if the context is canceled.
	Set(ctx context.Context, key K, value V) error

	// Delete removes a key-value pair from the cache.
	// Returns true if the key existed and was deleted, false otherwise.
	// Returns context.Canceled if the context is canceled.
	Delete(ctx context.Context, key K) (bool, error)

	// Clear removes all entries from the cache.
	// Returns context.Canceled if the context is canceled.
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
	Healthy() error
}
