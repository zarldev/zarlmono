// Package search provides search-engine tools the agent can call.
//
// The tool surface is one LLM contract — web_search — implemented by
// swappable backends (SearXNG, Brave, and future providers). Each backend owns
// its transport and response parsing; the shared Args, Hit, and Result types
// keep the model-facing schema and output shape identical across backends so a
// provider swap never retrains the tool contract.
package search

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// ToolName is the registered name surfaced to the LLM.
const ToolName tools.ToolName = "web_search"

// DefaultMaxResults caps the number of hits returned to the LLM when the
// caller doesn't specify max_results. Tuned for token budget: 10 results at
// ~150 chars each is ~1.5k chars of context per call.
const DefaultMaxResults = 10

// HardMaxResults is the upper bound on max_results. Backends return ~10-40
// results per page; clamping prevents the LLM from asking for 1000 and busting
// the context window.
const HardMaxResults = 25

// requestTimeout is the per-call ceiling on a backend round trip. Generous on
// first invocation because some upstreams routinely take 4-6 seconds.
const requestTimeout = 8 * time.Second

// Args is the typed argument struct every web_search backend decodes. Field
// tags drive both JSON decoding and SchemaFor schema generation — doc tags
// supply the LLM-facing descriptions.
type Args struct {
	Query      string             `json:"query" doc:"Search query. Plain text; the backend handles tokenisation."`
	MaxResults int                `json:"max_results,omitempty" doc:"Max results to return. Default 10, capped at 25."`
	Output     tools.OutputFormat `json:"output,omitempty" enum:"labeled,json" doc:"Output format: \"labeled\" (default, numbered title/URL/snippet rows) or \"json\"."`
}

// Hit is one normalized search result: the human-readable trio every backend
// reduces its transport shape to.
type Hit struct {
	URL     string `json:"url"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

// Result is web_search's structured Data plus the requested output mode. A
// consumer renders from Hits directly; the model sees String(): numbered
// labelled rows or the JSON payload, per Output.
type Result struct {
	Query       string
	Hits        []Hit
	Suggestions []string
	Output      tools.OutputFormat
}

// compactResponse is the post-trim JSON payload shape.
type compactResponse struct {
	Query       string   `json:"query"`
	Results     []Hit    `json:"results"`
	Suggestions []string `json:"suggestions,omitempty"`
}

// String renders the model-facing form for the requested output mode.
func (r Result) String() string {
	if r.Output == tools.OutputJSON {
		b, err := json.Marshal(compactResponse{
			Query:       r.Query,
			Results:     r.Hits,
			Suggestions: r.Suggestions,
		})
		if err != nil {
			return "{}"
		}
		return string(b)
	}
	return renderLabeled(r.Query, r.Hits, r.Suggestions)
}

// page is the normalized pre-render payload a backend fetch returns. The
// shared search handler turns it into a Result by attaching the requested
// output mode.
type page struct {
	query       string
	hits        []Hit
	suggestions []string
}

// specFor builds the shared LLM-facing spec; backends differ only in the
// description fragment naming their transport.
func specFor(description string) tools.ToolSpec {
	return tools.ToolSpec{
		Name:            ToolName,
		WorkspaceAccess: tools.WorkspaceAccesses.NONE,
		Description:     description,
		Parameters:      tools.SchemaFor[Args](),
	}
}

// classifyStatus maps a non-200 HTTP status to the typed failure the runner
// routes on: 5xx is transient (retryable), everything else is validation.
func classifyStatus(status int) *tools.Error {
	reason := fmt.Sprintf("http %d %s", status, http.StatusText(status))
	if status >= 500 {
		return tools.Transient(ToolName.String(), fmt.Errorf("%s", reason))
	}
	return tools.Validation(ToolName.String(), reason)
}

// clampMaxResults applies the default + hard cap. Zero (unset) becomes the
// default; negative values become the default; over-large values become the
// hard cap. The LLM can't accidentally flood its context.
func clampMaxResults(n int) int {
	if n <= 0 {
		return DefaultMaxResults
	}
	if n > HardMaxResults {
		return HardMaxResults
	}
	return n
}

// renderLabeled formats hits as the canonical labelled-output shape — header
// (count + query echo), blank-line separated numbered triples, optional
// trailing suggestions line.
//
// Why this shape: it mirrors the way every browser, IDE and CLI search tool
// presents results, so the model has the strongest training prior on it.
// Numbering also lets the model reference a result by index in its next turn
// ("based on result 2..."), which the JSON form couldn't do without burning
// tokens on `results[2].title`.
func renderLabeled(query string, hits []Hit, suggestions []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "results: %d  query: %s\n", len(hits), query)
	if len(hits) == 0 {
		b.WriteString("\n(no results)")
		if len(suggestions) > 0 {
			b.WriteString("\nsuggestions: ")
			b.WriteString(strings.Join(suggestions, ", "))
		}
		return b.String()
	}
	for i, r := range hits {
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

// oneLine collapses internal whitespace (newlines, tabs, repeated spaces) so
// each result row stays on its own visual line. Backends occasionally return
// content with embedded newlines that would otherwise break the indentation
// contract.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}
