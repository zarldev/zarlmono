package search_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/search"
)

// fakeSearxng returns an httptest.Server that responds to /search
// with the given body + status. The handler captures the inbound
// query so tests can assert URL composition.
func fakeSearxng(t *testing.T, status int, body string, gotQuery *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if gotQuery != nil {
			*gotQuery = r.URL.Query().Get("q")
		}
		if got := r.URL.Query().Get("format"); got != "json" {
			t.Errorf("format = %q, want json", got)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// runTyped invokes the SearxngTool with a typed SearxngArgs struct.
// It JSON-round-trips the struct into a ToolParameters map so the
// call exercises the same DecodeArgs path the LLM runtime uses,
// while letting tests stay readable with field literals.
func runTyped(t *testing.T, tool *search.SearxngTool, args search.SearxngArgs) *tools.ToolResult {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	var params tools.ToolParameters
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatalf("unmarshal args into map: %v", err)
	}
	res, err := tool.Execute(t.Context(), tools.ToolCall{
		ID:        "call-1",
		ToolName:  search.ToolName,
		Arguments: params,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return res
}

// searxText renders the model-facing string for a result — the same text
// the runner flattens SearxngResult to via fmt.Stringer.
func searxText(t *testing.T, res *tools.ToolResult) string {
	t.Helper()
	r, ok := res.Data.(search.SearxngResult)
	if !ok {
		t.Fatalf("Data is %T, want search.SearxngResult", res.Data)
	}
	return r.String()
}

func TestSearxngTool_DefinitionShape(t *testing.T) {
	t.Parallel()
	tool := search.New("http://localhost:9999")
	spec := tool.Definition()
	if spec.Name != search.ToolName {
		t.Errorf("Name = %q, want %q", spec.Name, search.ToolName)
	}
	props, ok := spec.Parameters.Map()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("Parameters.properties missing or wrong shape: %T", spec.Parameters.Map()["properties"])
	}
	if _, ok := props["query"]; !ok {
		t.Errorf("missing query property")
	}
	required, ok := spec.Parameters.Map()["required"].([]string)
	if !ok || len(required) == 0 || required[0] != "query" {
		t.Errorf("required = %v, want [\"query\"]", spec.Parameters.Map()["required"])
	}
}

func TestSearxngTool_HappyPath(t *testing.T) {
	t.Parallel()
	body := `{
		"query": "qwen mtp",
		"number_of_results": 2,
		"results": [
			{"url": "https://example.com/1", "title": "first", "content": "snippet one"},
			{"url": "https://example.com/2", "title": "second", "content": "snippet two"}
		],
		"suggestions": ["qwen 3.6 mtp", "qwen3.6 baseline"]
	}`
	var gotQuery string
	srv := fakeSearxng(t, http.StatusOK, body, &gotQuery)
	tool := search.New(srv.URL)

	res := runTyped(t, tool, search.SearxngArgs{Query: "qwen mtp", Output: tools.OutputJSON})
	if !res.Success {
		t.Fatalf("expected success, got error %q", res.Error)
	}
	if gotQuery != "qwen mtp" {
		t.Errorf("upstream q = %q, want %q", gotQuery, "qwen mtp")
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(searxText(t, res)), &out); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	results, _ := out["results"].([]any)
	if len(results) != 2 {
		t.Errorf("results = %d, want 2 (in body)", len(results))
	}
	sug, _ := out["suggestions"].([]any)
	if len(sug) != 2 {
		t.Errorf("suggestions = %d, want 2", len(sug))
	}
}

func TestSearxngTool_MaxResultsClamp(t *testing.T) {
	t.Parallel()
	// Build a 30-result body and verify the tool returns at most
	// HardMaxResults regardless of what the caller asks for.
	var results []map[string]any
	for range 30 {
		results = append(results, map[string]any{
			"url":     "https://example.com/x",
			"title":   "t",
			"content": "c",
		})
	}
	bodyBytes, _ := json.Marshal(map[string]any{
		"query":             "x",
		"number_of_results": 30,
		"results":           results,
	})
	srv := fakeSearxng(t, http.StatusOK, string(bodyBytes), nil)
	tool := search.New(srv.URL)

	// Float and string coercion were features of the pre-typed
	// stringly-accessor path; under the typed contract the JSON
	// decoder handles int natively and integer-valued floats land in
	// int fields fine, but quoted-string numerics no longer coerce —
	// the model is expected to emit a typed number.
	cases := []struct {
		name string
		arg  int
		want int
	}{
		{"unset_uses_default", 0, search.DefaultMaxResults},
		{"negative_uses_default", -5, search.DefaultMaxResults},
		{"small_value_honoured", 3, 3},
		{"over_cap_clamped", 9999, search.HardMaxResults},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			res := runTyped(t, tool, search.SearxngArgs{Query: "x", MaxResults: c.arg, Output: tools.OutputJSON})
			if !res.Success {
				t.Fatalf("error: %s", res.Error)
			}
			var out map[string]any
			_ = json.Unmarshal([]byte(searxText(t, res)), &out)
			got := len(out["results"].([]any))
			if got != c.want {
				t.Errorf("results=%d, want %d", got, c.want)
			}
		})
	}
}

func TestSearxngTool_MissingQuery(t *testing.T) {
	t.Parallel()
	tool := search.New("http://localhost:9999")
	res := runTyped(t, tool, search.SearxngArgs{})
	if res.Success {
		t.Errorf("expected failure ToolResult")
	}
	if !strings.Contains(res.Error, "query is required") {
		t.Errorf("error = %q, want mention of required query", res.Error)
	}
}

func TestSearxngTool_EmptyBaseURL(t *testing.T) {
	t.Parallel()
	tool := search.New("")
	res := runTyped(t, tool, search.SearxngArgs{Query: "x"})
	if res.Success {
		t.Errorf("expected failure for unconfigured tool")
	}
	if !strings.Contains(res.Error, "no SearXNG URL configured") {
		t.Errorf("error = %q, want config error", res.Error)
	}
}

func TestSearxngTool_Non200(t *testing.T) {
	t.Parallel()
	srv := fakeSearxng(t, http.StatusInternalServerError, `boom`, nil)
	tool := search.New(srv.URL)
	res := runTyped(t, tool, search.SearxngArgs{Query: "x"})
	if res.Success {
		t.Errorf("expected failure on 500")
	}
}

func TestSearxngTool_MalformedJSON(t *testing.T) {
	t.Parallel()
	srv := fakeSearxng(t, http.StatusOK, "not json", nil)
	tool := search.New(srv.URL)
	res := runTyped(t, tool, search.SearxngArgs{Query: "x"})
	if res.Success {
		t.Errorf("expected failure on malformed json")
	}
	if !strings.Contains(res.Error, "decode") {
		t.Errorf("error = %q, want decode error", res.Error)
	}
}

func TestSearxngTool_ContextCancellation(t *testing.T) {
	t.Parallel()
	// Slow server that blocks forever — context cancellation must
	// unblock the request without hanging the test.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	tool := search.New(srv.URL)

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // pre-cancel
	res, err := tool.Execute(ctx, tools.ToolCall{
		ID:        "call-1",
		ToolName:  search.ToolName,
		Arguments: tools.ToolParameters{"query": "x"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Success {
		t.Errorf("expected failure on cancelled context")
	}
	// http.Client.Do returns a url.Error wrapping context.Canceled.
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Errorf("ctx.Err() = %v, want context.Canceled", ctx.Err())
	}
}

// --- default (labelled) output ---------------------------------------

// Canonical reference for the labelled web_search shape: header line
// (results: N  query: X), blank-separated numbered triples (title /
// URL / snippet), optional trailing suggestions line. If this drifts
// to JSON the convention has slipped.
func TestSearxng_DefaultOutputShape(t *testing.T) {
	t.Parallel()
	srv := fakeSearxng(t, http.StatusOK, `{
		"query": "golang error wrapping",
		"results": [
			{"url":"https://go.dev/blog/go1.13-errors","title":"Working with Errors in Go 1.13","content":"The Go 1.13 release introduces new features that improve error handling."},
			{"url":"https://dev.to/example","title":"Error wrapping in Go","content":"Using fmt.Errorf with %w lets callers."}
		],
		"suggestions": ["errors.Is", "errors.As"]
	}`, nil)
	tool := search.New(srv.URL)
	res := runTyped(t, tool, search.SearxngArgs{Query: "golang error wrapping"})
	if res == nil || !res.Success {
		t.Fatalf("Execute: %+v", res)
	}
	body := searxText(t, res)
	if strings.HasPrefix(body, "[") || strings.HasPrefix(body, "{") {
		t.Errorf("output looks JSON-shaped (starts with bracket): %q", body)
	}
	if !strings.HasPrefix(body, "results: 2  query: golang error wrapping") {
		t.Errorf("header should report count + query echo: %q", body)
	}
	for _, want := range []string{"1. Working with Errors", "https://go.dev/blog/go1.13-errors", "2. Error wrapping", "suggestions: errors.Is, errors.As"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %q in output: %q", want, body)
		}
	}
}

// Multi-line snippets get collapsed onto one row so the indentation
// contract holds — a result with an embedded newline would otherwise
// look like an extra result to the model.
func TestSearxng_CollapsesMultilineSnippets(t *testing.T) {
	t.Parallel()
	srv := fakeSearxng(t, http.StatusOK, `{
		"query": "q",
		"results": [
			{"url":"https://x","title":"A\nB","content":"line one\nline two\nline three"}
		]
	}`, nil)
	tool := search.New(srv.URL)
	res := runTyped(t, tool, search.SearxngArgs{Query: "q"})
	body := searxText(t, res)
	if !strings.Contains(body, "1. A B") {
		t.Errorf("title not collapsed onto one line: %q", body)
	}
	if strings.Contains(body, "line one\nline two") {
		t.Errorf("snippet not collapsed: %q", body)
	}
	if !strings.Contains(body, "line one line two line three") {
		t.Errorf("snippet missing or mangled: %q", body)
	}
}

// Zero results → "(no results)" sentinel so the model can distinguish
// it from an error. Suggestions still emitted so the model can retry.
func TestSearxng_NoResultsSentinel(t *testing.T) {
	t.Parallel()
	srv := fakeSearxng(t, http.StatusOK, `{
		"query": "blefuscu",
		"results": [],
		"suggestions": ["blefuscu correct spelling"]
	}`, nil)
	tool := search.New(srv.URL)
	res := runTyped(t, tool, search.SearxngArgs{Query: "blefuscu"})
	if !res.Success {
		t.Fatalf("want success on zero results, got %+v", res)
	}
	body := searxText(t, res)
	if !strings.Contains(body, "results: 0") {
		t.Errorf("count header missing: %q", body)
	}
	if !strings.Contains(body, "(no results)") {
		t.Errorf("no-results sentinel missing: %q", body)
	}
	if !strings.Contains(body, "suggestions: blefuscu correct spelling") {
		t.Errorf("suggestions still emitted on zero results: %q", body)
	}
}

// 5xx is classified as transient — the runner uses this to decide
// whether to retry vs surface the error to the model.
func TestSearxng_FiveHundredIsTransient(t *testing.T) {
	t.Parallel()
	srv := fakeSearxng(t, http.StatusBadGateway, "", nil)
	tool := search.New(srv.URL)
	res := runTyped(t, tool, search.SearxngArgs{Query: "q"})
	if res.Success {
		t.Fatalf("5xx: want failure, got %+v", res)
	}
	if res.Err == nil || res.Err.Kind != tools.Kinds.TRANSIENT {
		t.Errorf("5xx should map to Kinds.TRANSIENT, got Err=%v", res.Err)
	}
}
