package retrieval_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/retrieval"
)

type store struct{ query retrieval.Query }

func (s *store) Index(context.Context, []retrieval.IndexedDocument) error { return nil }
func (s *store) Search(_ context.Context, q retrieval.Query) ([]retrieval.Document, error) {
	s.query = q
	return []retrieval.Document{{ID: "hit", Text: "match", Score: 1}}, nil
}

func TestTextChunkerSplitsWithOverlap(t *testing.T) {
	chunks, err := (retrieval.TextChunker{Size: 4, Overlap: 1}).Chunk(t.Context(), []retrieval.Document{{ID: "doc", Text: "abcdefg", Metadata: retrieval.Metadata{"source": "test"}}})
	if err != nil {
		t.Fatal(err)
	}
	got := []string{chunks[0].Text, chunks[1].Text}
	if !reflect.DeepEqual(got, []string{"abcd", "defg"}) {
		t.Fatalf("chunks = %#v", got)
	}
	if chunks[1].Metadata["parent_id"] != "doc" || chunks[1].ID != "doc:1" {
		t.Fatalf("metadata/id not propagated: %#v", chunks[1])
	}
}

func TestVectorRetrieverEmbedsAndSearches(t *testing.T) {
	st := &store{}
	r := retrieval.VectorRetriever{Limit: 3, Store: st, Embedder: retrieval.EmbedderFunc(func(_ context.Context, texts []string) ([]retrieval.Vector, error) {
		if !reflect.DeepEqual(texts, []string{"hello"}) {
			t.Fatalf("texts = %#v", texts)
		}
		return []retrieval.Vector{{1, 2}}, nil
	})}
	docs, err := r.Retrieve(t.Context(), "hello", retrieval.WithLimit(1), retrieval.WithFilter(map[string]any{"k": "v"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].ID != "hit" {
		t.Fatalf("docs = %#v", docs)
	}
	if st.query.Limit != 1 || st.query.Filter["k"] != "v" || !reflect.DeepEqual(st.query.Vector, retrieval.Vector{1, 2}) {
		t.Fatalf("query = %#v", st.query)
	}
}

func TestPipelineIndexesIntoMemoryVectorStore(t *testing.T) {
	store := retrieval.NewMemoryVectorStore()
	pipe := retrieval.Pipeline{
		Chunker: retrieval.TextChunker{Size: 5},
		Store:   store,
		Embedder: retrieval.EmbedderFunc(func(_ context.Context, texts []string) ([]retrieval.Vector, error) {
			vectors := make([]retrieval.Vector, 0, len(texts))
			for _, text := range texts {
				vectors = append(vectors, retrieval.Vector{float64(len(text)), 1})
			}
			return vectors, nil
		}),
	}
	if err := pipe.Index(t.Context(), []retrieval.Document{{ID: "doc", Text: "hello world", Metadata: retrieval.Metadata{"kind": "note"}}}); err != nil {
		t.Fatal(err)
	}
	hits, err := store.Search(t.Context(), retrieval.Query{Vector: retrieval.Vector{5, 1}, Limit: 1, Filter: map[string]any{"kind": "note"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Metadata["parent_id"] != "doc" {
		t.Fatalf("hits = %#v", hits)
	}
}
