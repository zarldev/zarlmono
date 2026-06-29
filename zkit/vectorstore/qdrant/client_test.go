package qdrant_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/vectorstore/qdrant"
)

// --- Unit tests against an httptest.Server (always run) ---

func TestEnsureCollection_CreatesWhenMissing(t *testing.T) {
	t.Parallel()

	var (
		gotMethod string
		gotPath   string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// Pretend the collection doesn't exist.
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPut:
			gotMethod = r.Method
			gotPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	c := qdrant.NewClient(srv.URL)
	if err := c.EnsureCollection(t.Context(), "test_col", 8); err != nil {
		t.Fatalf("EnsureCollection: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("expected PUT, got %s", gotMethod)
	}
	if gotPath != "/collections/test_col" {
		t.Errorf("path = %q, want /collections/test_col", gotPath)
	}
}

func TestEnsureCollection_NoOpWhenExists(t *testing.T) {
	t.Parallel()

	puts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
		case http.MethodPut:
			puts++
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	c := qdrant.NewClient(srv.URL)
	if err := c.EnsureCollection(t.Context(), "exists", 4); err != nil {
		t.Fatalf("EnsureCollection: %v", err)
	}
	if puts != 0 {
		t.Errorf("expected zero PUTs when collection exists, got %d", puts)
	}
}

func TestSearch_DecodesScoredPoints(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/points/search") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": []map[string]any{
				{
					"id":      "p1",
					"score":   0.92,
					"payload": map[string]any{"k": "v1"},
					"vector":  []float32{1, 0, 0, 0},
				},
				{
					"id":      "p2",
					"score":   0.41,
					"payload": map[string]any{"k": "v2"},
					"vector":  []float32{0, 1, 0, 0},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)

	c := qdrant.NewClient(srv.URL)
	got, err := c.Search(t.Context(), qdrant.SearchRequest{
		Collection: "memories",
		Vector:     []float32{1, 0, 0, 0},
		Limit:      2,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != "p1" || got[0].Score < 0.91 {
		t.Errorf("first result = %+v", got[0])
	}
	if got[0].Payload["k"] != "v1" {
		t.Errorf("payload = %v", got[0].Payload)
	}
}

func TestUpsert_SerializesPayload(t *testing.T) {
	t.Parallel()

	var seen map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&seen)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c := qdrant.NewClient(srv.URL)
	err := c.Upsert(t.Context(), "col", []qdrant.Point{
		{ID: "1", Vector: []float32{0, 1}, Payload: map[string]any{"kind": "alpha"}},
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	pts, _ := seen["points"].([]any)
	if len(pts) != 1 {
		t.Fatalf("expected 1 point in body, got %d", len(pts))
	}
	first, _ := pts[0].(map[string]any)
	if first["id"] != "1" {
		t.Errorf("id = %v", first["id"])
	}
	if pl, ok := first["payload"].(map[string]any); !ok || pl["kind"] != "alpha" {
		t.Errorf("payload = %v", first["payload"])
	}
}

func TestNonSuccessStatusReturnsError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	t.Cleanup(srv.Close)

	c := qdrant.NewClient(srv.URL)
	_, err := c.Search(t.Context(), qdrant.SearchRequest{Collection: "x", Limit: 1})
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention status code: %v", err)
	}
}

// --- Integration test (skipped without a running Qdrant) ---

func TestIntegration_EnsureUpsertSearch(t *testing.T) {
	if testing.Short() {
		t.Skip("requires running Qdrant")
	}

	url := os.Getenv("QDRANT_URL")
	if url == "" {
		t.Skip("QDRANT_URL not set; skipping integration test")
	}

	ctx := t.Context()
	col := "test_integration"
	c := qdrant.NewClient(url)

	if err := c.EnsureCollection(ctx, col, 4); err != nil {
		t.Fatalf("EnsureCollection: %v", err)
	}
	if err := c.EnsureCollection(ctx, col, 4); err != nil {
		t.Fatalf("EnsureCollection (idempotent): %v", err)
	}

	idA := "00000000-0000-0000-0000-000000000001"
	idB := "00000000-0000-0000-0000-000000000002"
	points := []qdrant.Point{
		{ID: idA, Vector: []float32{1, 0, 0, 0}, Payload: map[string]any{"kind": "alpha"}},
		{ID: idB, Vector: []float32{0, 1, 0, 0}, Payload: map[string]any{"kind": "beta"}},
	}
	if err := c.Upsert(ctx, col, points); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	results, err := c.Search(ctx, qdrant.SearchRequest{
		Collection: col,
		Vector:     []float32{1, 0, 0, 0},
		Limit:      2,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 || results[0].ID != idA {
		t.Errorf("top result: got %+v, want %s", results, idA)
	}

	filter := &qdrant.Filter{
		Must: []qdrant.FieldCondition{
			{Key: "kind", Match: qdrant.MatchValue{Value: "beta"}},
		},
	}
	filtered, err := c.Search(ctx, qdrant.SearchRequest{
		Collection: col,
		Vector:     []float32{0, 1, 0, 0},
		Filter:     filter,
		Limit:      2,
	})
	if err != nil {
		t.Fatalf("Search with filter: %v", err)
	}
	if len(filtered) == 0 || filtered[0].ID != idB {
		t.Errorf("filtered top result: got %+v, want %s", filtered, idB)
	}

	if err := c.DeleteByID(ctx, col, idA); err != nil {
		t.Fatalf("DeleteByID: %v", err)
	}
}
