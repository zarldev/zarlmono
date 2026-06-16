package searxng_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zarlai/tools/searxng"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

func fakeServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		results := map[string]any{
			"results": []map[string]any{
				{"title": "Golang and Qdrant", "url": "https://example.com/golang-qdrant", "content": "How to use Qdrant with Go."},
				{"title": "Qdrant Go Client", "url": "https://example.com/qdrant-go", "content": "Official Qdrant Go client docs."},
				{"title": "Vector search in Go", "url": "https://example.com/vector-search", "content": "Building vector search with Go."},
			},
		}
		json.NewEncoder(w).Encode(results)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSearchTool(t *testing.T) {
	srv := fakeServer(t)
	tool := searxng.NewSearchTool(searxng.NewClient(srv.URL))

	result, err := tool.Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{"query": "golang qdrant"}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("unexpected tool failure: %s", result.Error)
	}
	content := service.ToolResultText(result)
	if !strings.Contains(content, "https://example.com/golang-qdrant") {
		t.Errorf("content missing first result URL: %q", content)
	}
	if !strings.Contains(content, "Golang and Qdrant") {
		t.Errorf("content missing first result title: %q", content)
	}
}

func TestSearchToolLimit(t *testing.T) {
	srv := fakeServer(t)
	tool := searxng.NewSearchTool(searxng.NewClient(srv.URL))

	result, err := tool.Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{
		"query":       "golang qdrant",
		"num_results": float64(1),
	}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("unexpected tool failure: %s", result.Error)
	}
	content := service.ToolResultText(result)
	if strings.Contains(content, "https://example.com/qdrant-go") {
		t.Errorf("content should only have 1 result, got second URL: %q", content)
	}
	if !strings.Contains(content, "https://example.com/golang-qdrant") {
		t.Errorf("content missing the one expected result: %q", content)
	}
}

func TestSearchToolEmptyQuery(t *testing.T) {
	srv := fakeServer(t)
	tool := searxng.NewSearchTool(searxng.NewClient(srv.URL))

	result, err := tool.Execute(t.Context(), tools.ToolCall{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Success {
		t.Fatalf("expected failure, got %q", service.ToolResultText(result))
	}
}
