package backends

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// Store is the persistence the registry needs for DB-backed custom
// providers and their per-provider settings. Each consumer supplies its
// own implementation (zarlcode: sqlite-backed; zarlai: its own store).
// Built-in providers never touch the Store — they come from
// BuiltinDefinitions. A nil Store is valid: the registry then serves
// built-ins only and the mutating methods report ErrNoStore.
type Store interface {
	// ListProviders returns the persisted custom provider rows.
	ListProviders(ctx context.Context) ([]StoredProvider, error)
	// UpsertProvider inserts or replaces a custom provider.
	UpsertProvider(ctx context.Context, p StoredProvider) error
	// DeleteProvider removes a custom provider by name.
	DeleteProvider(ctx context.Context, name string) error
}

// StoredProvider is the registry's neutral view of a persisted custom
// provider. DB-encoding concerns (JSON-encoded seed lists, integer
// bools, timestamps) stay behind the Store implementation and never
// cross this boundary.
type StoredProvider struct {
	Name         string
	DisplayName  string
	AdapterType  string
	BaseURL      string
	DefaultModel string
	SeedModels   []string
	// ReasoningHistory is how prior-turn assistant reasoning is echoed back
	// in request history. See ProviderDefinition.ReasoningHistory.
	ReasoningHistory llm.ReasoningHistory
	// ContextWindow is the provider's declared context window in tokens; 0
	// means unknown (fall back to the table/probe/default).
	ContextWindow int
	// InputCostPerMTok / OutputCostPerMTok are the token price in USD per
	// 1,000,000 tokens (how providers publish it). 0 means unmetered/unknown.
	InputCostPerMTok  float64
	OutputCostPerMTok float64
	Enabled           bool
	Builtin           bool
}
