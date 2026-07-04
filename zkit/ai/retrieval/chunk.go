package retrieval

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Chunker splits documents into smaller documents suitable for embedding.
type Chunker interface {
	Chunk(ctx context.Context, docs []Document) ([]Document, error)
}

// ChunkFunc adapts a function to Chunker.
type ChunkFunc func(context.Context, []Document) ([]Document, error)

// Chunk calls f itself.
func (f ChunkFunc) Chunk(ctx context.Context, docs []Document) ([]Document, error) {
	return f(ctx, docs)
}

// TextChunker splits text by rune count with optional overlap. It preserves
// document metadata and annotates chunks with chunk_index, chunk_start, and
// chunk_end rune offsets.
type TextChunker struct {
	Size    int
	Overlap int
}

// Chunk splits each document by Size runes. Empty documents are skipped.
func (c TextChunker) Chunk(ctx context.Context, docs []Document) ([]Document, error) {
	if c.Size <= 0 {
		return nil, errors.New("chunk text: size must be positive")
	}
	if c.Overlap < 0 || c.Overlap >= c.Size {
		return nil, errors.New("chunk text: overlap must be non-negative and smaller than size")
	}
	var out []Document
	for _, doc := range docs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		text := strings.TrimSpace(doc.Text)
		if text == "" {
			continue
		}
		runes := []rune(text)
		step := c.Size - c.Overlap
		for start, idx := 0, 0; start < len(runes); start, idx = start+step, idx+1 {
			end := min(start+c.Size, len(runes))
			chunk := doc.Clone()
			chunk.Text = string(runes[start:end])
			chunk.Score = 0
			if chunk.Metadata == nil {
				chunk.Metadata = Metadata{}
			}
			chunk.Metadata["chunk_index"] = idx
			chunk.Metadata["chunk_start"] = start
			chunk.Metadata["chunk_end"] = end
			if doc.ID != "" {
				chunk.Metadata["parent_id"] = string(doc.ID)
				chunk.ID = DocumentID(fmt.Sprintf("%s:%d", doc.ID, idx))
			}
			out = append(out, chunk)
			if end == len(runes) {
				break
			}
		}
	}
	return out, nil
}
