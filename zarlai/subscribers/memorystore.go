package subscribers

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zarlai/tools/memory"
	"github.com/zarldev/zarlmono/zkit/vectorstore/qdrant"
)

// QdrantMemoryStore implements MemoryStore using Qdrant vector storage.
type QdrantMemoryStore struct {
	q *qdrant.Client
	e memory.Embedder
}

func NewQdrantMemoryStore(q *qdrant.Client, e memory.Embedder) *QdrantMemoryStore {
	return &QdrantMemoryStore{q: q, e: e}
}

func (s *QdrantMemoryStore) LoadMemories(ctx context.Context, personName string, limit int) ([]string, error) {
	return memory.LoadRecentMemories(ctx, s.q, s.e, personName, limit)
}

// StoreMemory embeds and stores a fact, skipping near-duplicates (cosine > 0.85).
func (s *QdrantMemoryStore) StoreMemory(ctx context.Context, personName, fact string) error {
	vec, err := s.e.Embed(ctx, fact)
	if err != nil {
		return fmt.Errorf("embed fact: %w", err)
	}

	// Check for near-duplicates.
	results, err := s.q.Search(ctx, qdrant.SearchRequest{
		Collection: memory.Collection,
		Vector:     vec,
		Filter: &qdrant.Filter{
			Must: []qdrant.FieldCondition{
				{Key: "person_name", Match: qdrant.MatchValue{Value: personName}},
			},
		},
		Limit: 1,
	})
	if err == nil && len(results) > 0 && results[0].Score > 0.85 {
		return nil // Skip duplicate.
	}

	point := qdrant.Point{
		ID:     uuid.New().String(),
		Vector: vec,
		Payload: map[string]any{
			"person_name": personName,
			"fact":        fact,
			"created_at":  "",
		},
	}
	if err := s.q.Upsert(ctx, memory.Collection, []qdrant.Point{point}); err != nil {
		return fmt.Errorf("upsert memory: %w", err)
	}
	return nil
}
