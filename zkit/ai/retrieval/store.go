package retrieval

import "context"

// IndexedDocument couples a document with its precomputed vector for storage.
type IndexedDocument struct {
	Document Document `json:"document"`
	Vector   Vector   `json:"vector"`
}

// Query carries vector-search inputs and optional metadata filters. Store
// implementations define the supported filter value shapes.
type Query struct {
	Vector Vector         `json:"vector"`
	Limit  int            `json:"limit,omitempty"`
	Filter map[string]any `json:"filter,omitempty"`
}

// VectorStore persists embedded documents and searches them by vector.
type VectorStore interface {
	Index(ctx context.Context, docs []IndexedDocument) error
	Search(ctx context.Context, query Query) ([]Document, error)
}
