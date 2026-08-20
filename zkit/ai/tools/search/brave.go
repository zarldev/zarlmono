package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/options"
	"github.com/zarldev/zarlmono/zkit/zhttp"
)

// braveBaseURL is the Brave Search API origin; fetch appends the web-search
// path. Authentication rides in the X-Subscription-Token header rather than
// URL params.
const braveBaseURL = "https://api.search.brave.com"

// braveDescription is the LLM-facing description for the Brave backend.
const braveDescription = "Search the web via the Brave Search API. Returns labelled plaintext — numbered " +
	"results with title, URL, and snippet rows; set output=\"json\" for {query, results:[{title,url,content}]} " +
	"instead. Use this for current information, post-cutoff facts, or to verify uncertain claims."

// WithBaseURL overrides the Brave API base URL (default api.search.brave.com).
// Tests point it at a local fake server.
func WithBaseURL(base string) options.Option[braveBackend] {
	return func(b *braveBackend) { b.baseURL = base }
}

// NewBrave returns the web_search tool backed by the Brave Search API. An
// empty apiKey is allowed at construction — the error surfaces at Execute time
// so the shell can still register the tool without failing startup.
func NewBrave(apiKey string, opts ...options.Option[braveBackend]) tools.Tool {
	b := &braveBackend{
		apiKey:  apiKey,
		baseURL: braveBaseURL,
		client:  zhttp.NewClient(zhttp.WithTimeout(requestTimeout)),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(b)
		}
	}
	return tools.NewTyped(specFor(braveDescription), b.search)
}

type braveBackend struct {
	apiKey  string
	baseURL string
	client  *zhttp.Client
}

// search runs one query against the configured Brave Search API key.
func (b *braveBackend) search(ctx context.Context, args Args) (Result, error) {
	if b.apiKey == "" {
		return Result{}, tools.Fatal(ToolName.String(), errors.New("no Brave Search API key configured"))
	}
	if args.Query == "" {
		return Result{}, tools.Validation(ToolName.String(), "query is required")
	}
	p, err := b.fetch(ctx, args.Query, clampMaxResults(args.MaxResults))
	if err != nil {
		return Result{}, err
	}
	return Result{Query: p.query, Hits: p.hits, Suggestions: p.suggestions, Output: args.Output.Resolve()}, nil
}

// rawBraveResponse mirrors the subset of the Brave web-search JSON we surface:
// web.results[].{title,url,description}. The full payload has profile,
// meta_url, family_friendly, etc.; the LLM only needs the trio.
type rawBraveWebResult struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type rawBraveResponse struct {
	Web struct {
		Results []rawBraveWebResult `json:"results"`
	} `json:"web"`
}

// fetch runs the Brave round trip and returns the trimmed, normalized page.
// Descriptions are stripped of inline markup before they become Hit.Content.
func (b *braveBackend) fetch(ctx context.Context, query string, maxResults int) (page, error) {
	u, err := url.Parse(b.baseURL)
	if err != nil {
		return page{}, tools.Fatal(ToolName.String(), fmt.Errorf("parse brave base url: %w", err))
	}
	u.Path = "/res/v1/web/search"
	v := url.Values{}
	v.Set("q", query)
	v.Set("count", strconv.Itoa(maxResults))
	u.RawQuery = v.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return page{}, tools.Fatal(ToolName.String(), fmt.Errorf("build request: %w", err))
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", b.apiKey)
	res, err := b.client.Do(ctx, req)
	if err != nil {
		return page{}, tools.Transient(ToolName.String(), err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return page{}, classifyStatus(res.StatusCode)
	}
	var raw rawBraveResponse
	if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
		return page{}, tools.Fatal(ToolName.String(), fmt.Errorf("decode response: %w", err))
	}
	hits := make([]Hit, 0, len(raw.Web.Results))
	for _, r := range raw.Web.Results {
		hits = append(hits, Hit{URL: r.URL, Title: r.Title, Content: stripMarkup(r.Description)})
	}
	if len(hits) > maxResults {
		hits = hits[:maxResults]
	}
	return page{query: query, hits: hits}, nil
}

// htmlTag matches inline markup Brave embeds in description snippets (e.g.
// <strong>). Compiled once; regexp is safe for concurrent use.
var htmlTag = regexp.MustCompile(`<[^>]*>`)

// stripMarkup removes inline markup and collapses whitespace onto one line.
func stripMarkup(s string) string {
	return oneLine(htmlTag.ReplaceAllString(s, ""))
}
