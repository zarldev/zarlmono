package cache

import (
	"context"
	"sync"
)

var (
	_ Reader[string, any]     = (*MemoryCache[string, any])(nil)
	_ Writer[string, any]     = (*MemoryCache[string, any])(nil)
	_ ReadWriter[string, any] = (*MemoryCache[string, any])(nil)
	_ Cache[string, any]      = (*MemoryCache[string, any])(nil)
)

// MemoryCache is a thread-safe generic cache implementation.
// It provides concurrent access to a map with read-write locking for performance.
type MemoryCache[K comparable, V any] struct {
	mu   sync.RWMutex
	data map[K]V
}

// NewMemoryCache creates a new thread-safe memory cache with the specified key and value types.
func NewMemoryCache[K comparable, V any]() *MemoryCache[K, V] {
	return &MemoryCache[K, V]{
		data: make(map[K]V),
	}
}

// Set stores a key-value pair in the cache.
// If the key already exists, its value is updated.
func (c *MemoryCache[K, V]) Set(ctx context.Context, key K, value V) error {
	select {
	case <-ctx.Done():
		return ErrCanceled
	default:
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = value
	return nil
}

// Get retrieves the value associated with the given key.
// Returns ErrNotFound if the key does not exist.
func (c *MemoryCache[K, V]) Get(ctx context.Context, key K) (V, error) {
	select {
	case <-ctx.Done():
		var zero V
		return zero, ErrCanceled
	default:
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	value, exists := c.data[key]
	if !exists {
		var zero V
		return zero, ErrNotFound
	}
	return value, nil
}

// Delete removes a key-value pair from the cache.
// Returns true if the key existed and was deleted, false otherwise.
func (c *MemoryCache[K, V]) Delete(ctx context.Context, key K) (bool, error) {
	select {
	case <-ctx.Done():
		return false, ErrCanceled
	default:
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	_, exists := c.data[key]
	if exists {
		delete(c.data, key)
	}
	return exists, nil
}

// Len returns the number of entries in the cache.
func (c *MemoryCache[K, V]) Len(ctx context.Context) (int, error) {
	select {
	case <-ctx.Done():
		return 0, ErrCanceled
	default:
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.data), nil
}

// Clear removes all entries from the cache.
func (c *MemoryCache[K, V]) Clear(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ErrCanceled
	default:
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = make(map[K]V)
	return nil
}

// Healthy returns nil as memory cache is always healthy.
func (c *MemoryCache[K, V]) Healthy() error {
	return nil
}
