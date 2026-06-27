package retrieval

import (
	"context"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	airetrieval "github.com/zarldev/zarlmono/zkit/ai/retrieval"
)

// PromptSource retrieves context for a query found in PromptVars and renders it
// as a system-prompt fragment.
type PromptSource struct {
	Retriever airetrieval.Retriever
	QueryKey  string
	Options   []airetrieval.RetrieveOption
	Format    FormatOptions
}

// System retrieves context for vars[QueryKey]. QueryKey defaults to "query"
// and falls back to "prompt" when query is empty.
func (s PromptSource) System(ctx context.Context, vars runner.PromptVars) (string, error) {
	key := s.QueryKey
	if key == "" {
		key = "query"
	}
	query := strings.TrimSpace(vars.String(key))
	if query == "" && key != "prompt" {
		query = strings.TrimSpace(vars.String("prompt"))
	}
	if query == "" {
		return "", nil
	}
	docs, err := s.Retriever.Retrieve(ctx, query, s.Options...)
	if err != nil {
		return "", fmt.Errorf("retrieve prompt context: %w", err)
	}
	return FormatDocuments(docs, s.Format), nil
}
