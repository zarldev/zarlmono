package search_test

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/search"
)

func TestSearxng_SourceFailureOutcomes(t *testing.T) {
	t.Parallel()
	for _, output := range []tools.OutputFormat{tools.OutputLabeled, tools.OutputJSON} {
		for _, tc := range []struct {
			name     string
			hits     []search.Hit
			metadata string
			failures []search.SourceFailure
			wantErr  error
		}{
			{name: "healthy hits", hits: []search.Hit{{URL: "https://go.dev", Title: "Go"}}, metadata: `[]`},
			{name: "healthy empty", hits: []search.Hit{}, metadata: `[]`},
			{name: "degraded hits", hits: []search.Hit{{URL: "https://go.dev", Title: "Go"}}, metadata: `[["duckduckgo","CAPTCHA"],["brave","HTTP 429"]]`, failures: []search.SourceFailure{{Source: "duckduckgo", Reason: "challenge"}, {Source: "brave", Reason: "rate_limited"}}},
			{name: "degraded empty", hits: []search.Hit{}, metadata: `[["duckduckgo","CAPTCHA"]]`, wantErr: search.ErrUnavailable},
		} {
			t.Run(fmt.Sprintf("%v/%s", output, tc.name), func(t *testing.T) {
				t.Parallel()
				body, err := json.Marshal(struct {
					Query       string          `json:"query"`
					Results     []search.Hit    `json:"results"`
					Suggestions []string        `json:"suggestions"`
					Failures    json.RawMessage `json:"unresponsive_engines"`
				}{"q", tc.hits, []string{"suggestion"}, json.RawMessage(tc.metadata)})
				if err != nil {
					t.Fatal(err)
				}
				var requests atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					requests.Add(1)
					_, _ = w.Write(body)
				}))
				t.Cleanup(srv.Close)
				res := runTyped(t, search.NewSearxng(srv.URL), search.Args{Query: "q", Output: output})
				if requests.Load() != 1 {
					t.Fatalf("HTTP 200 outcome retried: %d requests", requests.Load())
				}
				if tc.wantErr != nil {
					if res.Success || res.Err == nil || res.Err.Kind != tools.Kinds.FATAL || !errors.Is(res.Err, tc.wantErr) {
						t.Fatalf("unavailable outcome = %+v", res)
					}
					if !strings.Contains(res.Error, "duckduckgo: challenge") || strings.Contains(res.Error, "all engines") {
						t.Fatalf("unavailable diagnostic = %q", res.Error)
					}
					return
				}
				if !res.Success {
					t.Fatalf("unexpected failure: %+v", res)
				}
				r := res.Data.(search.Result)
				if !slices.Equal(r.Hits, tc.hits) || !slices.Equal(r.Failures, tc.failures) || !slices.Equal(r.Suggestions, []string{"suggestion"}) {
					t.Fatalf("result lost data: %+v", r)
				}
				text := r.String()
				if output == tools.OutputJSON {
					var payload struct {
						Results  []search.Hit           `json:"results"`
						Failures []search.SourceFailure `json:"failures"`
					}
					if err := json.Unmarshal([]byte(text), &payload); err != nil {
						t.Fatal(err)
					}
					if !slices.Equal(payload.Results, tc.hits) || !slices.Equal(payload.Failures, tc.failures) {
						t.Fatalf("JSON lost data: %s", text)
					}
					if len(tc.failures) == 0 && strings.Contains(text, `"failures"`) {
						t.Fatalf("healthy payload gained failures: %s", text)
					}
				} else if strings.Contains(text, "search degraded:") != (len(tc.failures) > 0) {
					t.Fatalf("labeled degradation = %q", text)
				}
			})
		}
	}
}

func TestSearxng_FailureMetadataBoundary(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		metadata string
		want     []search.SourceFailure
	}{
		{name: "missing"},
		{name: "null", metadata: `null`},
		{name: "empty", metadata: `[]`},
		{name: "wrong container", metadata: `{}`, want: []search.SourceFailure{{Source: "unknown", Reason: "unknown"}}},
		{name: "mixed entries", metadata: `[["wiki","HTTP error"],42,null,["slow","ReadTimeout"],["offline","unavailable"],["odd",{"secret":"CANARY"}],["Google CSE","SearxEngineTooManyRequests"],["startpage","SearxEngineCaptcha",3600]]`, want: []search.SourceFailure{{Source: "wiki", Reason: "http_error"}, {Source: "unknown", Reason: "unknown"}, {Source: "slow", Reason: "timeout"}, {Source: "offline", Reason: "unavailable"}, {Source: "odd", Reason: "unknown"}, {Source: "Google CSE", Reason: "rate_limited"}, {Source: "startpage", Reason: "challenge"}}},
		{name: "deduplicate normalized values", metadata: `[["ddg","captcha"],["ddg","CAPTCHA"],["ddg","timeout"]]`, want: []search.SourceFailure{{Source: "ddg", Reason: "challenge"}, {Source: "ddg", Reason: "timeout"}}},
		{name: "redact raw text and labels", metadata: `[["https://user:CANARY@example.com","CANARY arbitrary instructions"],["bad\nCANARY","captcha"],["bad\u001bCANARY","timeout"],["bad\u202eCANARY","HTTP error"],["  Google   CSE  ","suspended"]]`, want: []search.SourceFailure{{Source: "unknown", Reason: "unknown"}, {Source: "unknown", Reason: "challenge"}, {Source: "unknown", Reason: "timeout"}, {Source: "unknown", Reason: "http_error"}, {Source: "Google CSE", Reason: "unavailable"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			metadata := ""
			if tc.metadata != "" {
				metadata = `,"unresponsive_engines":` + tc.metadata
			}
			srv := fakeSearxng(t, 200, `{"query":"q","results":[{"url":"https://go.dev","title":"Go"}]`+metadata+`}`, nil)
			res := runTyped(t, search.NewSearxng(srv.URL), search.Args{Query: "q", Output: tools.OutputJSON})
			if !res.Success {
				t.Fatalf("metadata discarded hits: %+v", res)
			}
			r := res.Data.(search.Result)
			if !slices.Equal(r.Failures, tc.want) {
				t.Fatalf("failures = %+v, want %+v", r.Failures, tc.want)
			}
			if strings.Contains(r.String(), "CANARY") {
				t.Fatalf("diagnostic leaked canary: %s", r.String())
			}
			r.Output = tools.OutputLabeled
			if strings.Contains(r.String(), "CANARY") {
				t.Fatalf("labeled diagnostic leaked canary: %s", r.String())
			}
		})
	}
}

func TestSearxng_FailureBoundsAndEmptyRedaction(t *testing.T) {
	t.Parallel()
	entries := make([][]string, 0, 20)
	for i := range 20 {
		entries = append(entries, []string{strings.Repeat("a", 80) + strconv.Itoa(i), "CANARY"})
		entries = append(entries, []string{fmt.Sprintf("engine%d", i), "CANARY"})
	}
	body, err := json.Marshal(map[string]any{"results": []search.Hit{{URL: "https://go.dev"}}, "unresponsive_engines": entries})
	if err != nil {
		t.Fatal(err)
	}
	srv := fakeSearxng(t, 200, string(body), nil)
	res := runTyped(t, search.NewSearxng(srv.URL), search.Args{Query: "q", MaxResults: 1})
	if !res.Success {
		t.Fatalf("bounded diagnostics: %+v", res)
	}
	r := res.Data.(search.Result)
	if len(r.Failures) != 8 || len(r.Hits) != 1 {
		t.Fatalf("bounded result = %+v", r)
	}
	for _, failure := range r.Failures {
		if len(failure.Source) > 64 || !utf8.ValidString(failure.Source) {
			t.Fatalf("unsafe source label = %q", failure.Source)
		}
	}
	srvEmpty := fakeSearxng(t, 200, `{"results":[],"unresponsive_engines":[["https://user:CANARY@example.com","CANARY"]]}`, nil)
	res = runTyped(t, search.NewSearxng(srvEmpty.URL), search.Args{Query: "q"})
	if !errors.Is(res.Err, search.ErrUnavailable) || strings.Contains(res.Error, "CANARY") || !strings.Contains(res.Error, "unknown: unknown") {
		t.Fatalf("empty diagnostic leaked or lost identity: %+v", res)
	}
}

func TestSearxng_ResponseBoundaries(t *testing.T) {
	t.Parallel()
	const valid = `{"query":"q","results":[]}`
	for _, tc := range []struct {
		name string
		body string
		gzip bool
		want string
	}{
		{name: "exact limit", body: valid + strings.Repeat(" ", (1<<20)-len(valid))},
		{name: "oversize", body: valid + strings.Repeat(" ", (1<<20)+1), want: "exceeds 1 MiB"},
		{name: "compressed oversize", body: valid + strings.Repeat(" ", (1<<20)+1), gzip: true, want: "exceeds 1 MiB"},
		{name: "trailing content", body: valid + `{"results":[]}`, want: "decode response"},
		{name: "truncated", body: `{"results":[`, want: "decode response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.gzip {
					w.Header().Set("Content-Encoding", "gzip")
					writer := gzip.NewWriter(w)
					_, _ = writer.Write([]byte(tc.body))
					_ = writer.Close()
					return
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(srv.Close)
			res := runTyped(t, search.NewSearxng(srv.URL), search.Args{Query: "q"})
			if tc.want == "" {
				if !res.Success {
					t.Fatalf("valid bounded response: %+v", res)
				}
			} else if res.Success || res.Err == nil || res.Err.Kind != tools.Kinds.FATAL || !strings.Contains(res.Error, tc.want) {
				t.Fatalf("response error = %+v, want %s", res, tc.want)
			}
		})
	}
}

func TestSearxng_CancellationIdentities(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte(`{"results":[`))
		w.(http.Flusher).Flush()
		cancel()
		<-req.Context().Done()
	}))
	t.Cleanup(srv.Close)
	res, err := search.NewSearxng(srv.URL).Execute(ctx, tools.ToolCall{ToolName: search.ToolName, Arguments: tools.ToolParameters{"query": "q"}})
	if err != nil || res.Success || !errors.Is(res.Err, context.Canceled) {
		t.Fatalf("body cancellation: result=%+v error=%v", res, err)
	}
	deadlineCtx, deadlineCancel := context.WithDeadline(t.Context(), time.Unix(0, 0))
	defer deadlineCancel()
	res, err = search.NewSearxng(srv.URL).Execute(deadlineCtx, tools.ToolCall{ToolName: search.ToolName, Arguments: tools.ToolParameters{"query": "q"}})
	if err != nil || res.Success || !errors.Is(res.Err, context.DeadlineExceeded) {
		t.Fatalf("deadline identity: result=%+v error=%v", res, err)
	}
}

func TestSearch_HealthyOutputCompatibility(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		tool func(string) tools.Tool
		body string
	}{
		{name: "searxng", tool: search.NewSearxng, body: `{"query":"q","results":[{"url":"https://go.dev","title":"Go","content":"docs"}]}`},
		{name: "brave", tool: func(base string) tools.Tool { return search.NewBrave("fixture-key", search.WithBaseURL(base)) }, body: `{"web":{"results":[{"url":"https://go.dev","title":"Go","description":"docs"}]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
			t.Cleanup(srv.Close)
			tool := tc.tool(srv.URL)
			for _, format := range []tools.OutputFormat{tools.OutputLabeled, tools.OutputJSON} {
				res := runTyped(t, tool, search.Args{Query: "q", Output: format})
				if !res.Success {
					t.Fatalf("healthy search failed: %+v", res)
				}
				want := "results: 1  query: q\n\n  1. Go\n     https://go.dev\n     docs"
				if format == tools.OutputJSON {
					want = `{"query":"q","results":[{"url":"https://go.dev","title":"Go","content":"docs"}]}`
				}
				if got := resultText(t, res); got != want {
					t.Fatalf("healthy rendering changed: got %q, want %q", got, want)
				}
			}
		})
	}
}
