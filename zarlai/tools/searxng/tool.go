package searxng

import (
	"context"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zarlai/service"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

const (
	defaultNumResults = 5
	maxNumResults     = 10
)

// SearchTool implements service.Tool for web search via SearXNG.
type SearchTool struct {
	client *Client
}

// NewSearchTool creates a SearchTool backed by the given client.
func NewSearchTool(client *Client) *SearchTool {
	return &SearchTool{client: client}
}

func (t *SearchTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "web_search",
		Description: "Search the live web (via SearXNG) for current, time-sensitive, or local information: news, prices, sports scores, weather, recent releases, company facts, anything that changes. Also the right call when the user asks \"look up…\", \"search for…\", \"what's the latest on…\", or names a site/product you don't know. Prefer wiki_search for stable encyclopaedic facts; prefer search_youtube when the user wants videos. Returns title + URL + snippet for each hit — cite URLs in your reply when relevant.",
		Parameters: service.Parameters{
			{Name: "query", Type: service.ParamString, Description: "Search terms. Short, keyword-style queries beat full sentences.", Required: true},
			{Name: "num_results", Type: service.ParamInteger, Description: "How many results to return (default 5, capped at 10).", Required: false},
		}.ToJSONSchema(),
	}
}

func (t *SearchTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	query := call.Arguments.String("query", "")
	if query == "" {
		return tools.Failure(call.ID, tools.Validation("web_search", "query is required")), nil
	}

	limit := defaultNumResults
	if n := call.Arguments.Int("num_results", 0); n > 0 {
		limit = n
	}
	if limit > maxNumResults {
		limit = maxNumResults
	}

	results, err := t.client.Search(ctx, query, limit)
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("web_search", fmt.Errorf("web search: %w", err))), nil
	}

	if len(results) == 0 {
		return tools.Success(call.ID, "No results found."), nil
	}

	var sb strings.Builder
	for i, r := range results {
		fmt.Fprintf(&sb, "%d. %s\n   %s\n   %s\n\n", i+1, r.Title, r.URL, r.Content)
	}
	return tools.Success(call.ID, strings.TrimRight(sb.String(), "\n")), nil
}
