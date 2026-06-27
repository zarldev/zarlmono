package retrieval

import (
	"context"
	"errors"
	"fmt"
)

// Indexer embeds and stores documents.
type Indexer interface {
	Index(ctx context.Context, docs []Document) error
}

// Pipeline embeds documents, optionally chunking them first, and writes them to
// a VectorStore.
type Pipeline struct {
	Chunker  Chunker
	Embedder Embedder
	Store    VectorStore
}

// Index chunks, embeds, and stores docs. If Chunker is nil, docs are embedded
// as provided.
func (p Pipeline) Index(ctx context.Context, docs []Document) error {
	if p.Embedder == nil {
		return errors.New("index retrieval documents: embedder is nil")
	}
	if p.Store == nil {
		return errors.New("index retrieval documents: store is nil")
	}
	var err error
	if p.Chunker != nil {
		docs, err = p.Chunker.Chunk(ctx, docs)
		if err != nil {
			return fmt.Errorf("chunk documents: %w", err)
		}
	}
	texts := make([]string, 0, len(docs))
	for _, doc := range docs {
		texts = append(texts, doc.Text)
	}
	vectors, err := p.Embedder.Embed(ctx, texts)
	if err != nil {
		return fmt.Errorf("embed documents: %w", err)
	}
	if len(vectors) != len(docs) {
		return fmt.Errorf("embed documents: got %d vectors, want %d", len(vectors), len(docs))
	}
	indexed := make([]IndexedDocument, 0, len(docs))
	for i, doc := range docs {
		indexed = append(indexed, IndexedDocument{Document: doc, Vector: vectors[i]})
	}
	if err := p.Store.Index(ctx, indexed); err != nil {
		return fmt.Errorf("store documents: %w", err)
	}
	return nil
}
