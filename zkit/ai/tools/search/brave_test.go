package search_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/search"
)

// braveRequest captures the parts of the inbound Brave request tests assert on.
type braveRequest struct {
	token string
	query string
	count string
}

// fakeBrave returns an httptest.Server that responds to the Brave web-search
// path and captures the subscription token, query, and count.
func fakeBrave(t *testing.T, status int, body string, got *braveRequest) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/res/v1/web/search" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if got != nil {
			got.token = r.Header.Get("X-Subscription-Token")
			got.query = r.URL.Query().Get("q")
			got.count = r.URL.Query().Get("count")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestBrave_DefinitionShape(t *testing.T) {
	t.Parallel()
	spec := search.NewBrave("key").Definition()
	if spec.Name != search.ToolName {
		t.Errorf("Name = %q, want %q", spec.Name, search.ToolName)
	}
	if !strings.Contains(spec.Description, "Brave Search API") {
		t.Errorf("Description should name the backend: %q", spec.Description)
	}
}

func TestBrave_HappyPath(t *testing.T) {
	t.Parallel()
	body := `{"web":{"results":[
		{"url":"https://example.com/1","title":"first","description":"Best <strong>Greek</strong> restaurants in San Francisco."},
		{"url":"https://example.com/2","title":"second","description":"snippet two"}
	]}}`
	var got braveRequest
	srv := fakeBrave(t, http.StatusOK, body, &got)
	tool := search.NewBrave("tvly-test-key", search.WithBaseURL(srv.URL))

	res := runTyped(t, tool, search.Args{Query: "greek food", MaxResults: 3, Output: tools.OutputJSON})
	if !res.Success {
		t.Fatalf("expected success, got error %q", res.Error)
	}
	if got.token != "tvly-test-key" {
		t.Errorf("X-Subscription-Token = %q, want %q", got.token, "tvly-test-key")
	}
	if got.query != "greek food" {
		t.Errorf("q = %q, want %q", got.query, "greek food")
	}
	if got.count != "3" {
		t.Errorf("count = %q, want 3", got.count)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	results, _ := out["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	first := results[0].(map[string]any)
	if got := first["content"]; got != "Best Greek restaurants in San Francisco." {
		t.Errorf("content = %q, want markup stripped + whitespace collapsed", got)
	}
	if got := first["title"]; got != "first" {
		t.Errorf("title = %q, want first", got)
	}
}

func TestBrave_MissingKey(t *testing.T) {
	t.Parallel()
	tool := search.NewBrave("")
	res := runTyped(t, tool, search.Args{Query: "x"})
	if res.Success {
		t.Errorf("expected failure for unconfigured tool")
	}
	if !strings.Contains(res.Error, "no Brave Search API key configured") {
		t.Errorf("error = %q, want config error", res.Error)
	}
}

func TestBrave_MissingQuery(t *testing.T) {
	t.Parallel()
	tool := search.NewBrave("key")
	res := runTyped(t, tool, search.Args{})
	if res.Success {
		t.Errorf("expected failure ToolResult")
	}
	if !strings.Contains(res.Error, "query is required") {
		t.Errorf("error = %q, want mention of required query", res.Error)
	}
}

func TestBrave_FiveHundredIsTransient(t *testing.T) {
	t.Parallel()
	srv := fakeBrave(t, http.StatusBadGateway, "", nil)
	tool := search.NewBrave("key", search.WithBaseURL(srv.URL))
	res := runTyped(t, tool, search.Args{Query: "q"})
	if res.Success {
		t.Fatalf("5xx: want failure, got %+v", res)
	}
	if res.Err == nil || res.Err.Kind != tools.Kinds.TRANSIENT {
		t.Errorf("5xx should map to Kinds.TRANSIENT, got Err=%v", res.Err)
	}
}

func TestBrave_MalformedJSON(t *testing.T) {
	t.Parallel()
	srv := fakeBrave(t, http.StatusOK, "not json", nil)
	tool := search.NewBrave("key", search.WithBaseURL(srv.URL))
	res := runTyped(t, tool, search.Args{Query: "x"})
	if res.Success {
		t.Errorf("expected failure on malformed json")
	}
	if !strings.Contains(res.Error, "decode") {
		t.Errorf("error = %q, want decode error", res.Error)
	}
}
