package wiki

import (
	"context"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zarlai/tools/memory"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/vectorstore/qdrant"
)

// Collection is the Qdrant collection name for Wikipedia passages.
const Collection = "wikipedia"

// SearchTool searches a local Wikipedia knowledge base via Qdrant.
type SearchTool struct {
	qdrant   *qdrant.Client
	embedder memory.Embedder
}

// NewSearchTool creates a SearchTool.
func NewSearchTool(q *qdrant.Client, e memory.Embedder) *SearchTool {
	return &SearchTool{qdrant: q, embedder: e}
}

func (t *SearchTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "wiki_search",
		Description: "Look up encyclopaedic facts in a local Wikipedia index: history, science, geography, notable people, definitions, concepts. Prefer this over web_search for stable/reference knowledge (\"who was…\", \"what is…\", \"when did…\", \"where is…\") — it's faster, offline, and already curated. Use web_search instead for current events, news, prices, or anything time-sensitive. Phrase the query as a topic or noun-phrase (\"Battle of Clontarf\", \"photosynthesis\", \"Ada Lovelace\") rather than a full question.",
		Parameters: service.Parameters{
			{Name: "query", Type: service.ParamString, Description: "Topic or noun phrase to look up.", Required: true},
			{Name: "num_results", Type: service.ParamInteger, Description: "How many passages to return (default 5, capped at 10).", Required: false},
		}.ToJSONSchema(),
	}
}

func (t *SearchTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	query := call.Arguments.String("query", "")
	if query == "" {
		return tools.Failure(call.ID, tools.Validation("wiki_search", "query is required")), nil
	}

	limit := 5
	if n := call.Arguments.Float("num_results", 0); n > 0 {
		limit = min(int(n), 10)
	}

	vec, err := t.embedder.Embed(ctx, query)
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("wiki_search", fmt.Errorf("embed query: %w", err))), nil
	}

	results, err := t.qdrant.Search(ctx, qdrant.SearchRequest{
		Collection: Collection,
		Vector:     vec,
		Limit:      limit,
	})
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("wiki_search", fmt.Errorf("search wikipedia: %w", err))), nil
	}

	if len(results) == 0 {
		return tools.Success(call.ID, "No Wikipedia passages found."), nil
	}

	var sb strings.Builder
	for i, r := range results {
		title, _ := r.Payload["title"].(string)
		section, _ := r.Payload["section"].(string)
		text, _ := r.Payload["text"].(string)
		fmt.Fprintf(&sb, "%d. [%s > %s] (score: %.2f)\n%s\n", i+1, title, section, r.Score, text)
	}

	return tools.Success(call.ID, strings.TrimRight(sb.String(), "\n")), nil
}
