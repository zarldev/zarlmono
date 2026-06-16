package docstore

import (
	"context"
	"maps"
)

// Metadata is a semantic type for document metadata.
type Metadata map[string]any

// GetString retrieves a metadata value as string.
func (m Metadata) GetString(key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// GetInt retrieves a metadata value as int.
func (m Metadata) GetInt(key string) int {
	if v, ok := m[key]; ok {
		if i, ok := v.(int); ok {
			return i
		}
	}
	return 0
}

// GetBool retrieves a metadata value as bool.
func (m Metadata) GetBool(key string) bool {
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// Set adds or updates a metadata value.
func (m Metadata) Set(key string, value any) {
	m[key] = value
}

// Has checks if a metadata key exists.
func (m Metadata) Has(key string) bool {
	_, ok := m[key]
	return ok
}

// Clone creates a copy of metadata.
func (m Metadata) Clone() Metadata {
	clone := make(Metadata, len(m))
	maps.Copy(clone, m)
	return clone
}

// Document must be implemented by stored types.
type Document interface {
	ID() string
	SetID(string)
}

// Operator represents a query predicate operator.
type Operator int

const (
	// OpEq matches documents where the field equals the value.
	OpEq Operator = iota
	// OpNe matches documents where the field does not equal the value.
	OpNe
	// OpGt matches documents where the field is greater than the value.
	OpGt
	// OpGte matches documents where the field is greater than or equal to the value.
	OpGte
	// OpLt matches documents where the field is less than the value.
	OpLt
	// OpLte matches documents where the field is less than or equal to the value.
	OpLte
	// OpIn matches documents where the field is one of the provided values.
	OpIn
	// OpNotIn matches documents where the field is none of the provided values.
	OpNotIn
	// OpExists matches documents based on whether the field is present.
	OpExists
	// OpRegex matches string fields using the backing store's regex support.
	OpRegex
)

// Store provides typed document operations.
type Store[T Document] interface {
	// Insert adds a new document
	Insert(ctx context.Context, doc T) (string, error)

	// InsertWithMetadata adds a document with metadata
	InsertWithMetadata(ctx context.Context, doc T, meta Metadata) (string, error)

	// Find retrieves a document by ID
	Find(ctx context.Context, id string) (T, error)

	// Update modifies an existing document
	Update(ctx context.Context, id string, doc T) error

	// Upsert inserts or updates a document
	Upsert(ctx context.Context, doc T) error

	// Delete removes a document
	Delete(ctx context.Context, id string) error

	// Query creates a new query builder
	Query(ctx context.Context) Query[T]

	// Count returns total number of documents
	Count(ctx context.Context) (int64, error)

	// Close shuts down the store
	Close() error
}

// Query provides fluent query building.
type Query[T Document] interface {
	// Where adds a filter condition
	Where(field string, op Operator, value any) Query[T]

	// WhereIn adds an "in" filter condition
	WhereIn(field string, values []any) Query[T]

	// OrderBy adds sorting
	OrderBy(field string, desc bool) Query[T]

	// Limit sets maximum results
	Limit(n int) Query[T]

	// Skip sets offset for results
	Skip(n int) Query[T]

	// Find executes query and returns all matching documents
	Find() ([]T, error)

	// FindOne executes query and returns first matching document
	FindOne() (T, error)

	// Count returns number of matching documents
	Count() (int64, error)

	// Cursor returns an iterator for large result sets
	Cursor() (Cursor[T], error)
}

// Cursor provides iteration over large result sets.
type Cursor[T Document] interface {
	// Next advances to the next document
	Next(ctx context.Context) bool

	// Decode retrieves current document
	Decode(doc T) error

	// Close releases cursor resources
	Close(ctx context.Context) error
}
