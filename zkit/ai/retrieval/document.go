package retrieval

import "maps"

// DocumentID identifies a document or chunk in a retrieval corpus.
type DocumentID string

// Metadata carries caller-owned JSON-shaped attributes such as source path, URL,
// author, collection, or chunk coordinates. Keep raw maps at store/provider
// edges; use this semantic wrapper in retrieval APIs so metadata does not
// masquerade as arbitrary tool arguments.
type Metadata map[string]any

// Set adds or updates a metadata value. Values should be JSON-serialisable when
// a store persists them.
func (m Metadata) Set(key string, value any) { m[key] = value }

// Get retrieves a metadata value.
func (m Metadata) Get(key string) (any, bool) {
	v, ok := m[key]
	return v, ok
}

// GetString retrieves a metadata value as a string.
func (m Metadata) GetString(key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// Has reports whether key is present.
func (m Metadata) Has(key string) bool {
	_, ok := m[key]
	return ok
}

// Document is the common payload exchanged by chunkers, indexers, retrievers,
// and rerankers. Score is optional and is normally set by retrieval or rerank
// implementations; higher scores mean better matches.
type Document struct {
	ID       DocumentID `json:"id,omitempty"`
	Text     string     `json:"text"`
	Metadata Metadata   `json:"metadata,omitempty"`
	Score    float64    `json:"score,omitempty"`
}

// Clone returns a shallow document copy with a copied metadata map so callers
// can annotate results without mutating store-owned state.
func (d Document) Clone() Document {
	out := d
	if d.Metadata != nil {
		out.Metadata = make(Metadata, len(d.Metadata))
		maps.Copy(out.Metadata, d.Metadata)
	}
	return out
}
