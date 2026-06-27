package retrieval

import "context"

// Reranker reorders or filters retrieved documents for a query.
type Reranker interface {
	Rerank(ctx context.Context, query string, docs []Document) ([]Document, error)
}

// RerankerFunc adapts a function to Reranker.
type RerankerFunc func(context.Context, string, []Document) ([]Document, error)

// Rerank calls f itself.
func (f RerankerFunc) Rerank(ctx context.Context, query string, docs []Document) ([]Document, error) {
	return f(ctx, query, docs)
}
