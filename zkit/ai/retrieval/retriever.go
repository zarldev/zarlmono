package retrieval

import (
	"context"
	"fmt"
)

// RetrieveOptions tune a retrieval call.
type RetrieveOptions struct {
	Limit  int
	Filter map[string]any
}

// RetrieveOption mutates RetrieveOptions.
type RetrieveOption func(*RetrieveOptions)

// WithLimit sets the maximum number of documents to return.
func WithLimit(limit int) RetrieveOption {
	return func(o *RetrieveOptions) { o.Limit = limit }
}

// WithFilter sets an implementation-defined metadata filter.
func WithFilter(filter map[string]any) RetrieveOption {
	return func(o *RetrieveOptions) { o.Filter = filter }
}

// Retriever finds relevant documents for a natural-language query.
type Retriever interface {
	Retrieve(ctx context.Context, query string, opts ...RetrieveOption) ([]Document, error)
}

// RetrieverFunc adapts a function to Retriever.
type RetrieverFunc func(context.Context, string, ...RetrieveOption) ([]Document, error)

// Retrieve calls f itself.
func (f RetrieverFunc) Retrieve(ctx context.Context, query string, opts ...RetrieveOption) ([]Document, error) {
	return f(ctx, query, opts...)
}

// VectorRetriever embeds a query and searches a VectorStore.
type VectorRetriever struct {
	Embedder Embedder
	Store    VectorStore
	Limit    int
}

// Retrieve embeds query and delegates vector search to Store.
func (r VectorRetriever) Retrieve(ctx context.Context, query string, opts ...RetrieveOption) ([]Document, error) {
	settings := RetrieveOptions{Limit: r.Limit}
	for _, opt := range opts {
		if opt != nil {
			opt(&settings)
		}
	}
	vectors, err := r.Embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(vectors) != 1 {
		return nil, fmt.Errorf("embed query: got %d vectors, want 1", len(vectors))
	}
	docs, err := r.Store.Search(ctx, Query{Vector: vectors[0], Limit: settings.Limit, Filter: settings.Filter})
	if err != nil {
		return nil, fmt.Errorf("search vector store: %w", err)
	}
	return docs, nil
}
