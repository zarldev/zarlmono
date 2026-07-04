// Package search provides search-engine tools the agent can call.
// The only implementation today is SearxngTool — an HTTP client for
// a local SearXNG instance. Pulled into its own package so future
// providers (Tavily, Brave, etc.) can land alongside without
// expanding pkg/ai/tools/code's surface (that package is for
// workspace-bound tools).
package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/zhttp"
)

// ToolName is the registered name surfaced to the LLM.
const ToolName tools.ToolName = "web_search"

// DefaultMaxResults caps the number of hits returned to the LLM
// when the caller doesn't specify max_results. Tuned for token
// budget: 10 results at ~150 chars each is ~1.5k chars of context
// per call.
const DefaultMaxResults = 10

// HardMaxResults is the upper bound on max_results. SearXNG itself
// returns ~10-40 results per page; clamping prevents the LLM from
// asking for 1000 and busting the context window.
const HardMaxResults = 25

// requestTimeout is the per-call ceiling on the SearXNG round trip.
// Generous on first invocation because some engines (Google
// scraping, Brave) routinely take 4-6 seconds.
const requestTimeout = 8 * time.Second

// SearxngTool queries a local SearXNG instance via its JSON API.
// Construct with [New]; the returned tool is safe for concurrent
// Execute calls (the embedded *zhttp.Client is.)
type SearxngTool struct {
	baseURL string
	client  *zhttp.Client
}

// SearxngArgs is the typed argument struct SearxngTool.Execute
// decodes into via tools.DecodeArgs. Field tags drive both JSON
// decoding and SchemaFor schema generation — doc tags supply the
// LLM-facing descriptions.
type SearxngArgs struct {
	Query      string             `json:"query" doc:"Search query. Plain text; SearXNG handles tokenisation."`
	MaxResults int                `json:"max_results,omitempty" doc:"Max results to return. Default 10, capped at 25."`
	Output     tools.OutputFormat `json:"output,omitempty" enum:"labeled,json" doc:"Output format: \"labeled\" (default, numbered title/URL/snippet rows) or \"json\"."`
}

// New returns a SearxngTool that talks to the SearXNG instance at
// baseURL (e.g. "http://127.0.0.1:8080"). An empty baseURL is
// allowed at construction — the error surfaces at Execute time so
// the shell can still register the tool and show the configured URL
// in /tools without failing startup.
//
// HTTP transport is [zhttp.Client] with the per-call request timeout
// applied — retry on transient 5xx / 429 + connection errors keeps
// the agent loop working when SearXNG is briefly restarting or rate-
// limiting one of its upstream engines.
func New(baseURL string) *SearxngTool {
	return &SearxngTool{
		baseURL: baseURL,
		client:  zhttp.NewClient(zhttp.WithTimeout(requestTimeout)),
	}
}

// Definition is the LLM-facing spec. Description nudges the model
// towards using this for fact-finding (post-knowledge-cutoff info,
// current docs, etc.) rather than guessing.
func (t *SearxngTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name: ToolName,
		Description: "Search the web via a local SearXNG instance. Returns labelled plaintext — numbered " +
			"results with title, URL, and snippet rows; set output=\"json\" for {query, results:[{title,url,content}]} " +
			"instead. Use this for current information, post-cutoff facts, or to verify uncertain claims.",
		Parameters: tools.SchemaFor[SearxngArgs](),
	}
}

// rawSearxngResult is the subset of SearXNG's per-result fields we
// surface. The full JSON has thumbnail, engine, template,
// parsed_url, positions, etc.; the LLM only needs the human-
// readable trio.
type rawSearxngResult struct {
	URL     string `json:"url"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

// rawSearxngResponse mirrors the SearXNG JSON shape. Suggestions
// piggyback because they're tiny and occasionally useful when the
// query had a typo — the LLM can decide to retry.
type rawSearxngResponse struct {
	Query           string             `json:"query"`
	NumberOfResults int                `json:"number_of_results"`
	Results         []rawSearxngResult `json:"results"`
	Suggestions     []string           `json:"suggestions,omitempty"`
}

// compactResponse is the post-trim JSON payload shape.
type compactResponse struct {
	Query       string             `json:"query"`
	Results     []rawSearxngResult `json:"results"`
	Suggestions []string           `json:"suggestions,omitempty"`
}

// SearxngResult is web_search's structured Data: the trimmed results plus
// the requested output mode. A consumer renders from Results directly; the
// model sees String(): numbered labelled rows or the JSON payload, per
// Output.
type SearxngResult struct {
	Query       string
	Results     []rawSearxngResult
	Suggestions []string
	Output      tools.OutputFormat
}

// String renders the model-facing form for the requested output mode.
func (r SearxngResult) String() string {
	if r.Output == tools.OutputJSON {
		b, err := json.Marshal(compactResponse{
			Query:       r.Query,
			Results:     r.Results,
			Suggestions: r.Suggestions,
		})
		if err != nil {
			return "{}"
		}
		return string(b)
	}
	return renderSearxngLabeled(r.Query, r.Results, r.Suggestions)
}

// Execute runs one query against the configured SearXNG instance.
// Errors fall into three buckets:
//   - configuration (no baseURL) → typed failure ToolResult
//   - validation (empty query)   → typed failure ToolResult
//   - transport / parsing        → typed failure ToolResult
//
// Returning err only on truly unexpected conditions (none right
// now) keeps the LLM's tool-call loop running — a failed search is
// a result the model can reason about, not a host-level error.
func (t *SearxngTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	args, failure := decodeAndValidate(t.baseURL, call)
	if failure != nil {
		return failure, nil
	}
	raw, failure := t.fetch(ctx, call.ID, args.Query, clampMaxResults(args.MaxResults))
	if failure != nil {
		return failure, nil
	}
	return tools.Success(call.ID, SearxngResult{
		Query:       raw.Query,
		Results:     raw.Results,
		Suggestions: raw.Suggestions,
		Output:      args.Output.Resolve(),
	}), nil
}

// decodeAndValidate runs the argument-decode + pre-flight checks.
// Returns the typed args on success, or a populated failure ToolResult
// ready to return to the runner.
func decodeAndValidate(baseURL string, call tools.ToolCall) (SearxngArgs, *tools.ToolResult) {
	args, derr := tools.DecodeArgs[SearxngArgs](call.Arguments)
	if derr != nil {
		return args, tools.Failure(call.ID, derr)
	}
	if baseURL == "" {
		return args, tools.Failure(call.ID, tools.Fatal("web_search", errors.New("no SearXNG URL configured")))
	}
	if args.Query == "" {
		return args, tools.Failure(call.ID, tools.Validation("web_search", "query is required"))
	}
	return args, nil
}

// fetch runs the SearXNG round trip and returns the trimmed
// response. On failure returns a populated *tools.ToolResult — the
// caller can hand it back to the runner unchanged.
func (t *SearxngTool) fetch(
	ctx context.Context,
	callID, query string,
	maxResults int,
) (rawSearxngResponse, *tools.ToolResult) {
	u, err := buildURL(t.baseURL, query)
	if err != nil {
		return rawSearxngResponse{}, tools.Failure(
			callID,
			tools.Validation("web_search", fmt.Sprintf("build url: %v", err)),
		)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return rawSearxngResponse{}, tools.Failure(
			callID,
			tools.Fatal("web_search", fmt.Errorf("build request: %w", err)),
		)
	}
	req.Header.Set("User-Agent", "zarlcode/web_search")
	res, err := t.client.Do(ctx, req)
	if err != nil {
		return rawSearxngResponse{}, tools.Failure(callID, tools.Transient("web_search", err))
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		if res.StatusCode >= 500 {
			return rawSearxngResponse{}, tools.Failure(
				callID,
				tools.Transient("web_search", fmt.Errorf("%s", res.Status)),
			)
		}
		return rawSearxngResponse{}, tools.Failure(callID, tools.Validation("web_search", res.Status))
	}
	var raw rawSearxngResponse
	if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
		return rawSearxngResponse{}, tools.Failure(
			callID,
			tools.Fatal("web_search", fmt.Errorf("decode response: %w", err)),
		)
	}
	if len(raw.Results) > maxResults {
		raw.Results = raw.Results[:maxResults]
	}
	return raw, nil
}

// buildURL composes the search endpoint. SearXNG accepts q + format
// as the minimum required pair; safesearch and language come from
// the server's settings.yml.
func buildURL(base, query string) (string, error) {
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

// clampMaxResults applies the default + hard cap. Zero (unset)
// becomes the default; negative values become the default; over-
// large values become the hard cap. The LLM can't accidentally
// flood its context.
func clampMaxResults(n int) int {
	if n <= 0 {
		return DefaultMaxResults
	}
	if n > HardMaxResults {
		return HardMaxResults
	}
	return n
}

// renderSearxngLabeled formats a SearXNG response as the canonical
// labelled-output shape — header (count + query echo), blank line
// separated numbered triples, optional trailing suggestions line.
//
// Why this shape: it mirrors the way every browser, IDE and CLI
// search tool presents results, so the model has the strongest
// training prior on it. Numbering also lets the model reference a
// result by index in its next turn ("based on result 2..."), which
// the JSON form couldn't do without burning tokens on
// `results[2].title`.
func renderSearxngLabeled(query string, results []rawSearxngResult, suggestions []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "results: %d  query: %s\n", len(results), query)
	if len(results) == 0 {
		b.WriteString("\n(no results)")
		if len(suggestions) > 0 {
			b.WriteString("\nsuggestions: ")
			b.WriteString(strings.Join(suggestions, ", "))
		}
		return b.String()
	}
	for i, r := range results {
		b.WriteString("\n")
		fmt.Fprintf(&b, "  %d. %s\n", i+1, oneLine(r.Title))
		fmt.Fprintf(&b, "     %s\n", r.URL)
		if r.Content != "" {
			fmt.Fprintf(&b, "     %s\n", oneLine(r.Content))
		}
	}
	if len(suggestions) > 0 {
		b.WriteString("\nsuggestions: ")
		b.WriteString(strings.Join(suggestions, ", "))
	}
	return strings.TrimRight(b.String(), "\n")
}

// oneLine collapses internal whitespace (newlines, tabs, repeated
// spaces) so each result row stays on its own visual line. SearXNG
// occasionally returns content with embedded newlines that would
// otherwise break the indentation contract.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}
