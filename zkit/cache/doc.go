// Package cache defines generic cache interfaces with memory, file-backed, and
// Redis-backed implementations.
//
// The core interfaces are intentionally small: Reader, Writer, ReadWriter, and
// Cache. Concrete adapters currently live in this package; if dependency
// pressure grows, the Redis implementation is a candidate for a subpackage such
// as cache/redis.
package cache
