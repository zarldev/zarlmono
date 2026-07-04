package retrieval

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
)

// MemoryVectorStore is an in-memory VectorStore using cosine similarity. It is
// intended for tests, examples, and small local corpora; durable applications
// should provide a store backed by Qdrant, pgvector, SQLite, or another index.
type MemoryVectorStore struct {
	mu   sync.RWMutex
	docs []IndexedDocument
}

// NewMemoryVectorStore returns an empty MemoryVectorStore.
func NewMemoryVectorStore() *MemoryVectorStore { return &MemoryVectorStore{} }

// Index appends documents to the in-memory corpus. Documents and vectors are
// copied so callers can reuse or mutate their inputs after indexing.
func (s *MemoryVectorStore) Index(ctx context.Context, docs []IndexedDocument) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	copied := make([]IndexedDocument, 0, len(docs))
	for _, doc := range docs {
		if len(doc.Vector) == 0 {
			return fmt.Errorf("index document %q: vector is empty", doc.Document.ID)
		}
		copied = append(copied, cloneIndexed(doc))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs = append(s.docs, copied...)
	return nil
}

// Search returns the nearest indexed documents. When Query.Filter is nil, all
// indexed documents are considered. Query.Filter requires exact equality on
// document metadata keys for equality conditions.
func (s *MemoryVectorStore) Search(ctx context.Context, query Query) ([]Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(query.Vector) == 0 {
		return nil, errors.New("search vector store: query vector is empty")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	hits := make([]Document, 0, len(s.docs))
	for _, indexed := range s.docs {
		if !query.Filter.Match(indexed.Document.Metadata) {
			continue
		}
		score, ok := cosine(query.Vector, indexed.Vector)
		if !ok {
			continue
		}
		doc := indexed.Document.Clone()
		doc.Score = score
		hits = append(hits, doc)
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if query.Limit > 0 && query.Limit < len(hits) {
		hits = hits[:query.Limit]
	}
	return hits, nil
}

func cloneIndexed(doc IndexedDocument) IndexedDocument {
	out := IndexedDocument{Document: doc.Document.Clone()}
	if doc.Vector != nil {
		out.Vector = append(Vector(nil), doc.Vector...)
	}
	return out
}

func cosine(a, b Vector) (float64, bool) {
	if len(a) != len(b) || len(a) == 0 {
		return 0, false
	}
	var dot, an, bn float64
	for i := range a {
		dot += a[i] * b[i]
		an += a[i] * a[i]
		bn += b[i] * b[i]
	}
	if an == 0 || bn == 0 {
		return 0, false
	}
	return dot / (math.Sqrt(an) * math.Sqrt(bn)), true
}
