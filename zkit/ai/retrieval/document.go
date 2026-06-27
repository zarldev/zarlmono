package retrieval

// DocumentID identifies a document or chunk in a retrieval corpus.
type DocumentID string

// Metadata carries caller-owned attributes such as source path, URL, author,
// collection, or chunk coordinates. Values should be JSON-serialisable when a
// store persists them, but the core interface keeps the type open.
type Metadata map[string]any

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
		for k, v := range d.Metadata {
			out.Metadata[k] = v
		}
	}
	return out
}
