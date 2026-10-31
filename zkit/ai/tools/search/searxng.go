package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/zhttp"
)

// searxngDescription is the LLM-facing description for the SearXNG backend.
const searxngDescription = "Search the web via a local SearXNG instance. Returns labelled plaintext — numbered " +
	"results with title, URL, and snippet rows; set output=\"json\" for {query, results:[{title,url,content}]} " +
	"instead. Partial results include source failures; no hits with reported source failures is an unavailable search, not proof of no matches. " +
	"Use this for current information, post-cutoff facts, or to verify uncertain claims."

// NewSearxng returns the web_search tool backed by the SearXNG instance at
// baseURL (e.g. "http://127.0.0.1:8080"). An empty baseURL is allowed at
// construction — the error surfaces at Execute time so the shell can still
// register the tool and show the configured URL in /tools without failing
// startup.
//
// HTTP transport is zhttp.Client with the per-call request timeout applied —
// retry on transient HTTP 5xx / 429 and connection errors handles restarts.
// Source failures inside an HTTP 200 response are reported without retrying.
func NewSearxng(baseURL string) tools.Tool {
	b := &searxngBackend{
		baseURL: baseURL,
		client:  zhttp.NewClient(zhttp.WithTimeout(requestTimeout)),
	}
	return tools.New(specFor(searxngDescription), b.search)
}

type searxngBackend struct {
	baseURL string
	client  *zhttp.Client
}

// search runs one query against the configured SearXNG instance.
func (b *searxngBackend) search(ctx context.Context, args Args) (Result, error) {
	if b.baseURL == "" {
		return Result{}, tools.Fatal(ToolName.String(), errors.New("no SearXNG URL configured"))
	}
	if args.Query == "" {
		return Result{}, tools.Validation(ToolName.String(), "query is required")
	}
	p, err := b.fetch(ctx, args.Query, clampMaxResults(args.MaxResults))
	if err != nil {
		return Result{}, err
	}
	return Result{Query: p.query, Hits: p.hits, Suggestions: p.suggestions, Failures: p.failures, Output: args.Output.Resolve()}, nil
}

// rawSearxngResponse mirrors the SearXNG JSON shape. Suggestions piggyback
// because they're tiny and occasionally useful when the query had a typo — the
// LLM can decide to retry. Results decode directly into Hit, the shared
// normalized shape.
type rawSearxngResponse struct {
	Query               string          `json:"query"`
	NumberOfResults     int             `json:"number_of_results"`
	Results             []Hit           `json:"results"`
	Suggestions         []string        `json:"suggestions,omitempty"`
	UnresponsiveEngines json.RawMessage `json:"unresponsive_engines"`
}

// fetch runs the SearXNG round trip and returns the trimmed, normalized page.
// Errors are classified (validation / transient / fatal) so the runner's retry
// and guardrail policy can route on Kind.
func (b *searxngBackend) fetch(ctx context.Context, query string, maxResults int) (page, error) {
	u, err := buildSearxngURL(b.baseURL, query)
	if err != nil {
		return page{}, tools.Validation(ToolName.String(), fmt.Sprintf("build url: %v", err))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return page{}, tools.Fatal(ToolName.String(), fmt.Errorf("build request: %w", err))
	}
	req.Header.Set("User-Agent", "zarlcode/web_search")
	res, err := b.client.Do(ctx, req)
	if err != nil {
		return page{}, tools.Transient(ToolName.String(), err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return page{}, classifyStatus(res.StatusCode)
	}
	const maxResponseBytes = 1 << 20
	body, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
	if err != nil {
		return page{}, tools.Transient(ToolName.String(), fmt.Errorf("read response: %w", err))
	}
	if len(body) > maxResponseBytes {
		return page{}, tools.Fatal(ToolName.String(), errors.New("search response exceeds 1 MiB"))
	}
	var raw rawSearxngResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return page{}, tools.Fatal(ToolName.String(), fmt.Errorf("decode response: %w", err))
	}
	if len(raw.Results) > maxResults {
		raw.Results = raw.Results[:maxResults]
	}
	failures := sourceFailures(raw.UnresponsiveEngines)
	if len(raw.Results) == 0 && len(failures) > 0 {
		return page{}, tools.Fatal(ToolName.String(), fmt.Errorf("%w (%s)", ErrUnavailable, failureSummary(failures)))
	}
	return page{query: raw.Query, hits: raw.Results, suggestions: raw.Suggestions, failures: failures}, nil
}

// buildSearxngURL composes the search endpoint. SearXNG accepts q + format as
// the minimum required pair; safesearch and language come from the server's
// settings.yml.
func buildSearxngURL(base, query string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse base %q: %w", base, err)
	}
	u.Path = "/search"
	v := url.Values{}
	v.Set("q", query)
	v.Set("format", "json")
	u.RawQuery = v.Encode()
	return u.String(), nil
}
