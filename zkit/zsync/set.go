package zsync

import (
	"cmp"
	"slices"
)

// Set is a thread-safe generic set built on Map[T, struct{}].
type Set[T comparable] struct {
	m *Map[T, struct{}]
}

// NewSet creates a new thread-safe set containing values.
func NewSet[T comparable](values ...T) *Set[T] {
	set := &Set[T]{m: NewMap[T, struct{}]()}
	for _, value := range values {
		set.Add(value)
	}
	return set
}

// Add inserts a value. No-op if already present.
func (s *Set[T]) Add(value T) {
	s.m.Set(value, struct{}{})
}

// AddAll inserts values atomically. Existing values are unchanged.
func (s *Set[T]) AddAll(values ...T) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if s.m.data == nil {
		s.m.data = make(map[T]struct{}, len(values))
	}
	for _, value := range values {
		s.m.data[value] = struct{}{}
	}
}

// AddIfAbsent inserts value when it is not already present.
// It reports whether this call inserted the value.
func (s *Set[T]) AddIfAbsent(value T) bool {
	_, loaded := s.m.LoadOrStore(value, struct{}{})
	return !loaded
}

// Contains reports whether value is in the set.
func (s *Set[T]) Contains(value T) bool {
	_, err := s.m.Get(value)
	return err == nil
}

// Remove deletes a value. Returns true if it existed.
func (s *Set[T]) Remove(value T) bool {
	return s.m.Delete(value)
}

// RemoveAll removes values atomically.
func (s *Set[T]) RemoveAll(values ...T) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	for _, value := range values {
		delete(s.m.data, value)
	}
}

// Len returns the current member count.
func (s *Set[T]) Len() int { return s.m.Len() }

// Values returns a snapshot of all values. Order is not guaranteed.
func (s *Set[T]) Values() []T { return s.m.Keys() }

// Clear removes all members.
func (s *Set[T]) Clear() { s.m.Clear() }

// Ordered returns a sorted snapshot of values for any ordered T.
func Ordered[T cmp.Ordered](s *Set[T]) []T {
	values := s.Values()
	slices.Sort(values)
	return values
}

// Ordered returns values sorted by the provided comparator.
func (s *Set[T]) Ordered(cmpFn func(a, b T) int) []T {
	values := s.m.Keys()
	slices.SortFunc(values, cmpFn)
	return values
}
