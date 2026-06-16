package memory_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zarlai/tools/memory"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/vectorstore/qdrant"
)

// fakeEmbedder returns a zero vector of fixed length.
type fakeEmbedder struct{}

func (f *fakeEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return make([]float32, 4), nil
}

// fakeQdrantServer is an in-memory Qdrant-shaped HTTP server.
type fakeQdrantServer struct {
	mu     sync.Mutex
	points map[string]map[string]any // id -> payload
}

func newFakeQdrantServer(t *testing.T) (*fakeQdrantServer, *httptest.Server) {
	t.Helper()
	fqs := &fakeQdrantServer{points: make(map[string]map[string]any)}
	srv := httptest.NewServer(fqs)
	t.Cleanup(srv.Close)
	return fqs, srv
}

func (s *fakeQdrantServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	switch {
	// GET /collections/{name} — collection check
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/collections/"):
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"status": "ok"})

	// PUT /collections/{name}/points — upsert
	case r.Method == http.MethodPut && strings.HasSuffix(path, "/points"):
		var body struct {
			Points []struct {
				ID      string         `json:"id"`
				Vector  []float32      `json:"vector"`
				Payload map[string]any `json:"payload"`
			} `json:"points"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		for _, p := range body.Points {
			s.points[p.ID] = p.Payload
		}
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"status": "ok"})

	// POST /collections/{name}/points/search — search
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/points/search"):
		var req struct {
			Filter *struct {
				Must []struct {
					Key   string `json:"key"`
					Match struct {
						Value any `json:"value"`
					} `json:"match"`
				} `json:"must"`
			} `json:"filter"`
			Limit int `json:"limit"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		s.mu.Lock()
		defer s.mu.Unlock()

		type resultItem struct {
			ID      string         `json:"id"`
			Score   float32        `json:"score"`
			Payload map[string]any `json:"payload"`
			Vector  []float32      `json:"vector"`
		}
		var results []resultItem
		for id, payload := range s.points {
			if req.Filter != nil {
				match := true
				for _, cond := range req.Filter.Must {
					v, _ := payload[cond.Key].(string)
					want, _ := cond.Match.Value.(string)
					if v != want {
						match = false
						break
					}
				}
				if !match {
					continue
				}
			}
			results = append(results, resultItem{
				ID:      id,
				Score:   0.95,
				Payload: payload,
			})
			if req.Limit > 0 && len(results) >= req.Limit {
				break
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"result": results})

	// POST /collections/{name}/points/delete — delete by id or filter
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/points/delete"):
		var body struct {
			Points []string `json:"points"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		for _, id := range body.Points {
			delete(s.points, id)
		}
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"status": "ok"})

	default:
		http.NotFound(w, r)
	}
}

func personCtx(t *testing.T) context.Context {
	t.Helper()
	return context.WithValue(t.Context(), service.CtxPersonName, "Bruno")
}

func TestRememberTool(t *testing.T) {
	_, srv := newFakeQdrantServer(t)
	q := qdrant.NewClient(srv.URL)
	e := &fakeEmbedder{}

	tool := memory.NewRememberTool(q, e)

	result, err := tool.Execute(personCtx(t), tools.ToolCall{Arguments: tools.ToolParameters{"fact": "loves Go"}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("unexpected tool failure: %s", result.Error)
	}
	content := service.ToolResultText(result)
	if !strings.Contains(content, "Remembered") {
		t.Errorf("content %q should contain 'Remembered'", content)
	}
	if !strings.Contains(content, "Bruno") {
		t.Errorf("content %q should contain 'Bruno'", content)
	}
	if !strings.Contains(content, "loves Go") {
		t.Errorf("content %q should contain 'loves Go'", content)
	}
}

func TestRememberToolNoPerson(t *testing.T) {
	_, srv := newFakeQdrantServer(t)
	q := qdrant.NewClient(srv.URL)
	e := &fakeEmbedder{}

	tool := memory.NewRememberTool(q, e)

	result, err := tool.Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{"fact": "loves Go"}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("unexpected tool failure: %s", result.Error)
	}
	if service.ToolResultText(result) == "" {
		t.Fatal("expected soft message when no person in context")
	}
}

func TestRecallTool(t *testing.T) {
	_, srv := newFakeQdrantServer(t)
	q := qdrant.NewClient(srv.URL)
	e := &fakeEmbedder{}
	ctx := personCtx(t)

	// First remember a fact.
	remember := memory.NewRememberTool(q, e)
	r, err := remember.Execute(ctx, tools.ToolCall{Arguments: tools.ToolParameters{"fact": "loves Go"}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !r.Success {
		t.Fatalf("remember failed: %s", r.Error)
	}

	// Now recall.
	recall := memory.NewRecallTool(q, e)
	result, err := recall.Execute(ctx, tools.ToolCall{Arguments: tools.ToolParameters{"query": "programming language"}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("recall failed: %s", result.Error)
	}
	content := service.ToolResultText(result)
	if !strings.Contains(content, "loves Go") {
		t.Errorf("content %q should contain 'loves Go'", content)
	}
}

func TestForgetTool(t *testing.T) {
	_, srv := newFakeQdrantServer(t)
	q := qdrant.NewClient(srv.URL)
	e := &fakeEmbedder{}
	ctx := personCtx(t)

	// First remember a fact.
	remember := memory.NewRememberTool(q, e)
	r, err := remember.Execute(ctx, tools.ToolCall{Arguments: tools.ToolParameters{"fact": "loves Go"}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !r.Success {
		t.Fatalf("remember failed: %s", r.Error)
	}

	// Forget it.
	forget := memory.NewForgetTool(q, e)
	result, err := forget.Execute(ctx, tools.ToolCall{Arguments: tools.ToolParameters{"query": "loves Go"}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("forget failed: %s", result.Error)
	}
	content := service.ToolResultText(result)
	if !strings.Contains(content, "Forgot") {
		t.Errorf("content %q should contain 'Forgot'", content)
	}
}
