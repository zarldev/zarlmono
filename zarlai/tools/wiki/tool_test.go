package wiki_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zarlai/tools/wiki"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/vectorstore/qdrant"
)

// fakeEmbedder returns a zero 768-dim vector.
type fakeEmbedder struct{}

func (f *fakeEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return make([]float32, 768), nil
}

// newFakeQdrantServer returns an httptest.Server that serves one Wikipedia result on search.
func newFakeQdrantServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/points/search") {
			result := map[string]any{
				"result": []map[string]any{
					{
						"id":    "1",
						"score": float32(0.92),
						"payload": map[string]any{
							"title":   "Go (programming language)",
							"section": "History",
							"text":    "Go was designed at Google in 2007.",
						},
						"vector": []float32{},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(result)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestWikiSearchTool(t *testing.T) {
	srv := newFakeQdrantServer(t)
	q := qdrant.NewClient(srv.URL)
	e := &fakeEmbedder{}

	tool := wiki.NewSearchTool(q, e)

	result, err := tool.Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{"query": "Go programming language"}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("unexpected tool failure: %s", result.Error)
	}
	content := service.ToolResultText(result)
	if !strings.Contains(content, "Go (programming language)") {
		t.Errorf("content %q should contain article title", content)
	}
	if !strings.Contains(content, "Go was designed at Google in 2007.") {
		t.Errorf("content %q should contain article text", content)
	}
}

func TestWikiSearchToolEmptyQuery(t *testing.T) {
	srv := newFakeQdrantServer(t)
	q := qdrant.NewClient(srv.URL)
	e := &fakeEmbedder{}

	tool := wiki.NewSearchTool(q, e)

	result, err := tool.Execute(t.Context(), tools.ToolCall{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Success {
		t.Fatalf("expected failure, got %q", service.ToolResultText(result))
	}
}
