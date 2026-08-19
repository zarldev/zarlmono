package docstore

import (
	"context"
	"errors"
)

var (
	// ErrNotFound reports an operation for a record that does not exist.
	ErrNotFound = errors.New("document not found")
	// ErrConflict reports creation of a record whose ID already exists.
	ErrConflict = errors.New("document conflict")
	// ErrInvalidRecord reports a record without the required identity or value.
	ErrInvalidRecord = errors.New("invalid document record")
)

// ID identifies a document within a collection.
type ID string

// Value is a document value that can make an independent copy of itself.
// Stores call Clone before retaining or returning a value, so callers never
// share mutable state with a store.
type Value[T any] interface {
	Clone() T
}

// Record couples an explicit document identity with its domain value. Put any
// application metadata that affects its domain behavior in Value rather than a
// weakly typed side channel.
type Record[T Value[T]] struct {
	ID    ID
	Value T
}

// Page bounds a List result. A zero Limit returns all records after Offset.
type Page struct {
	Offset int
	Limit  int
}

// Valid reports whether the page can be applied to a list.
func (p Page) Valid() bool {
	return p.Offset >= 0 && p.Limit >= 0
}

// recordClone returns a record whose value belongs to the caller.
func recordClone[T Value[T]](record Record[T]) Record[T] {
	record.Value = record.Value.Clone()
	return record
}

// ensureContext returns cancellation before a store changes or observes state.
func ensureContext(ctx context.Context) error {
	return ctx.Err()
}

// DocumentStore is deliberately not declared here. Consumers should define the
// narrow persistence capability their domain needs.
var _ = context.Canceled
