package zsync

import "sync"

// Map is a thread-safe generic map with read-write locking. The zero
// value is ready to use — callers may embed `Map[K, V]` by value
// without an explicit constructor, which keeps "zero-value struct
// works" types like [Counter]-style call counters and per-key mutex
// dispensers idiomatic. Use [NewMap] only when an explicit
// allocation point reads more clearly.
//
// Do not copy a Map after first use — it embeds a [sync.RWMutex].
type Map[K comparable, V any] struct {
	mu   sync.RWMutex
	data map[K]V
}

// NewMap creates a new thread-safe map. The zero value of [Map] is
// also usable; this constructor exists for the cases where an
// explicit allocation site is clearer than relying on lazy init.
func NewMap[K comparable, V any]() *Map[K, V] {
	return &Map[K, V]{data: make(map[K]V)}
}

// Set stores a key/value pair. Existing values are overwritten.
func (m *Map[K, V]) Set(key K, value V) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		m.data = make(map[K]V)
	}
	m.data[key] = value
}

// LoadOrStore returns the existing value for key when present,
// otherwise stores value and returns it. The second return reports
// whether the value was loaded (true) or stored (false). Mirrors the
// stdlib [sync.Map.LoadOrStore] semantics but with generics, so
// callers don't pay the interface{} round-trip + type assertion that
// [sync.Map] forces. Useful for per-key dispensers (mutexes, atomic
// counters) where multiple goroutines race to initialise the same
// slot — exactly one wins; the losers get the winner's value back.
func (m *Map[K, V]) LoadOrStore(key K, value V) (V, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.data[key]; ok {
		return existing, true
	}
	if m.data == nil {
		m.data = make(map[K]V)
	}
	m.data[key] = value
	return value, false
}

// Get retrieves the value for key, or ErrNotFound if absent.
func (m *Map[K, V]) Get(key K) (V, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	value, ok := m.data[key]
	if !ok {
		var zero V
		return zero, ErrNotFound
	}
	return value, nil
}

// Delete removes a key. Returns true if the key existed.
func (m *Map[K, V]) Delete(key K) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.data[key]; !ok {
		return false
	}
	delete(m.data, key)
	return true
}

// Len returns the current entry count.
func (m *Map[K, V]) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.data)
}

// Keys returns a snapshot of all keys. Order is not guaranteed.
func (m *Map[K, V]) Keys() []K {
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys := make([]K, 0, len(m.data))
	for k := range m.data {
		keys = append(keys, k)
	}
	return keys
}

// Clear removes all entries.
func (m *Map[K, V]) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = make(map[K]V)
}
