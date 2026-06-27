package retrieval

import "context"

// Vector is an embedding vector. Implementations should document whether they
// normalise vectors and which distance metric their stores expect.
type Vector []float64

// Embedder converts text into embedding vectors. The output length must match
// the input length.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([]Vector, error)
}

// EmbedderFunc adapts a function to Embedder.
type EmbedderFunc func(context.Context, []string) ([]Vector, error)

// Embed calls f itself.
func (f EmbedderFunc) Embed(ctx context.Context, texts []string) ([]Vector, error) {
	return f(ctx, texts)
}
