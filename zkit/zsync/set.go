package zsync

import (
	"cmp"
	"slices"
)

// Set is a thread-safe generic set built on Map[T, struct{}].
type Set[T comparable] struct {
	m *Map[T, struct{}]
}

// NewSet creates a new thread-safe set.
func NewSet[T comparable]() *Set[T] {
	return &Set[T]{m: NewMap[T, struct{}]()}
}

// Add inserts a value. No-op if already present.
func (s *Set[T]) Add(value T) {
	s.m.Set(value, struct{}{})
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
