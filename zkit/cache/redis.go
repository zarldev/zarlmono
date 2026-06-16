package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/zarldev/zarlmono/zkit/options"
)

var (
	_ Reader[string, any]     = (*RedisCache[string, any])(nil)
	_ Writer[string, any]     = (*RedisCache[string, any])(nil)
	_ ReadWriter[string, any] = (*RedisCache[string, any])(nil)
	_ Cache[string, any]      = (*RedisCache[string, any])(nil)
)

// RedisCache is a thread-safe cache implementation using Redis as the backend.
// It provides distributed caching capabilities across multiple application instances.
type RedisCache[K comparable, V any] struct {
	client redis.UniversalClient
	prefix string
	ttl    time.Duration
}

// RedisOption configures a RedisCache during construction.
type RedisOption[K comparable, V any] = options.Option[RedisCache[K, V]]

// WithClient replaces the default localhost Redis client.
//
// Use this when callers need a cluster/universal client, custom address,
// credentials, TLS, test doubles, or externally managed client lifetime.
func WithClient[K comparable, V any](c redis.UniversalClient) RedisOption[K, V] {
	return func(rc *RedisCache[K, V]) {
		rc.client = c
	}
}

// WithPrefix scopes all keys used by this cache instance.
//
// Clear and Len operate only on keys with this prefix, so production callers
// should set a non-empty prefix when sharing a Redis database with other data.
func WithPrefix[K comparable, V any](pre string) RedisOption[K, V] {
	return func(rc *RedisCache[K, V]) {
		rc.prefix = pre
	}
}

// WithTTL sets the expiration applied to values written by Set.
//
// A zero TTL leaves values without an expiration, matching redis.Client.Set
// semantics.
func WithTTL[K comparable, V any](ttl time.Duration) RedisOption[K, V] {
	return func(rc *RedisCache[K, V]) {
		rc.ttl = ttl
	}
}

// NewRedisCache creates a new Redis-backed cache with the specified configuration.
func NewRedisCache[K comparable, V any](opts ...RedisOption[K, V]) *RedisCache[K, V] {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	rc := RedisCache[K, V]{
		client: client,
	}
	for _, opt := range opts {
		opt(&rc)
	}
	return &rc
}

// Set stores a key-value pair in Redis.
// If the key already exists, its value is updated.
func (c *RedisCache[K, V]) Set(ctx context.Context, key K, value V) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	redisKey, err := c.makeKey(key)
	if err != nil {
		return err
	}

	data, err := json.Marshal(value)
	if err != nil {
		return err
	}

	return c.client.Set(ctx, redisKey, data, c.ttl).Err()
}

// Get retrieves the value associated with the given key from Redis.
// Returns ErrNotFound if the key does not exist.
func (c *RedisCache[K, V]) Get(ctx context.Context, key K) (V, error) {
	select {
	case <-ctx.Done():
		var zero V
		return zero, ctx.Err()
	default:
	}

	redisKey, err := c.makeKey(key)
	if err != nil {
		var zero V
		return zero, err
	}

	result, err := c.client.Get(ctx, redisKey).Result()
	if err != nil {
		var zero V
		if errors.Is(err, redis.Nil) {
			return zero, ErrNotFound
		}
		return zero, err
	}

	var value V
	if err := json.Unmarshal([]byte(result), &value); err != nil {
		var zero V
		return zero, err
	}

	return value, nil
}

// Delete removes a key-value pair from Redis.
// Returns true if the key existed and was deleted, false otherwise.
func (c *RedisCache[K, V]) Delete(ctx context.Context, key K) (bool, error) {
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	default:
	}

	redisKey, err := c.makeKey(key)
	if err != nil {
		return false, err
	}

	result, err := c.client.Del(ctx, redisKey).Result()
	if err != nil {
		return false, err
	}

	return result > 0, nil
}

// ErrClearRequiresPrefix is returned by Clear when the cache was
// constructed without [WithPrefix]. An empty prefix would mean SCAN
// * across the whole selected Redis DB — almost certainly not what
// the caller intended, and a footgun for any cache sharing a DB
// with other data. Make the contract explicit: callers wanting
// "wipe everything" can issue FLUSHDB themselves.
var ErrClearRequiresPrefix = errors.New("redis cache: Clear requires a non-empty prefix (use WithPrefix)")

// clearBatchSize bounds the number of keys passed to DEL in a
// single command during Clear. Sized for typical Redis deployments
// (the server doesn't enforce a hard cap on argument count but very
// long DEL commands push the protocol buffer hard). Streamed
// deletion inside the scan also caps in-memory accumulation —
// earlier shape buffered every matched key before issuing a single
// DEL, which OOM'd on huge caches.
const clearBatchSize = 1024

// Clear removes all entries with the configured prefix from Redis.
//
// Refuses when no prefix is configured — see [ErrClearRequiresPrefix].
// Deletes are issued in batches of [clearBatchSize] as the scan
// produces keys, so the client never holds the full key set in
// memory.
func (c *RedisCache[K, V]) Clear(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if c.prefix == "" {
		return ErrClearRequiresPrefix
	}

	pattern := c.prefix + "*"

	iter := c.client.Scan(ctx, 0, pattern, 0).Iterator()
	batch := make([]string, 0, clearBatchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := c.client.Del(ctx, batch...).Err(); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}

	for iter.Next(ctx) {
		batch = append(batch, iter.Val())
		if len(batch) >= clearBatchSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}

	if err := iter.Err(); err != nil {
		return err
	}
	return flush()
}

// Len returns the approximate number of entries with the configured prefix in Redis.
func (c *RedisCache[K, V]) Len(ctx context.Context) (int, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	pattern := c.prefix + "*"

	iter := c.client.Scan(ctx, 0, pattern, 0).Iterator()
	count := 0

	for iter.Next(ctx) {
		count++
	}

	if err := iter.Err(); err != nil {
		return 0, err
	}

	return count, nil
}

// makeKey marshals key to JSON and prepends the cache's prefix.
// Returns the key + a marshal error so callers can fail loudly
// instead of silently collapsing every unmarshalable key into a
// prefix-only collision (which was the previous behaviour — the
// underscore-error from json.Marshal was thrown away and an empty
// string was concatenated, so every distinct unmarshalable key
// became the same Redis key).
func (c *RedisCache[K, V]) makeKey(key K) (string, error) {
	keyBytes, err := json.Marshal(key)
	if err != nil {
		return "", fmt.Errorf("marshal cache key: %w", err)
	}
	return c.prefix + string(keyBytes), nil
}

// Healthy checks if Redis is accessible by pinging it.
func (c *RedisCache[K, V]) Healthy() error {
	ctx := context.Background()
	result := c.client.Ping(ctx)
	if err := result.Err(); err != nil {
		return fmt.Errorf("ping redis: %w", err)
	}
	return nil
}
