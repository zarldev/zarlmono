package qdrant_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/vectorstore/qdrant"
	"github.com/zarldev/zarlmono/zkit/zhttp"
)

// --- Unit tests against an httptest.Server (always run) ---

func TestEnsureCollectionExistsCreatesWhenMissing(t *testing.T) {
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

	c := mustNewClient(t, srv.URL)
	if err := c.EnsureCollectionExists(t.Context(), "test_col", qdrant.CollectionConfig{
		Dimension: 8,
		Distance:  qdrant.Distances.COSINE,
	}); err != nil {
		t.Fatalf("EnsureCollectionExists: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("expected PUT, got %s", gotMethod)
	}
	if gotPath != "/collections/test_col" {
		t.Errorf("path = %q, want /collections/test_col", gotPath)
	}
}

func TestEnsureCollectionExistsDoesNotReconcileExistingCollection(t *testing.T) {
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

	c := mustNewClient(t, srv.URL)
	if err := c.EnsureCollectionExists(t.Context(), "exists", qdrant.CollectionConfig{
		Dimension: 4,
		Distance:  qdrant.Distances.COSINE,
	}); err != nil {
		t.Fatalf("EnsureCollectionExists: %v", err)
	}
	if puts != 0 {
		t.Errorf("expected zero PUTs when collection exists, got %d", puts)
	}
}

func TestEnsureCollectionExistsReturnsLookupFailure(t *testing.T) {
	t.Parallel()

	puts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			puts++
		}
		http.Error(w, "denied", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	c := mustNewClient(t, srv.URL)
	err := c.EnsureCollectionExists(t.Context(), "private", qdrant.CollectionConfig{Dimension: 4})
	if err == nil || !strings.Contains(err.Error(), "check collection") {
		t.Fatalf("EnsureCollectionExists error = %v, want lookup failure", err)
	}
	if puts != 0 {
		t.Errorf("PUT count = %d, want 0", puts)
	}
}

func TestEnsureCollectionExistsAcceptsConcurrentCreate(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusConflict)
	}))
	t.Cleanup(srv.Close)

	c := mustNewClient(t, srv.URL)
	if err := c.EnsureCollectionExists(t.Context(), "raced", qdrant.CollectionConfig{Dimension: 4}); err != nil {
		t.Fatalf("EnsureCollectionExists: %v", err)
	}
}

func TestSearch_DecodesScoredPoints(t *testing.T) {
	t.Parallel()
	var requestBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode search request: %v", err)
		}
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

	c := mustNewClient(t, srv.URL)
	got, err := c.Search(t.Context(), qdrant.SearchRequest{
		Collection: "memories",
		Vector:     []float32{1, 0, 0, 0},
		Limit:      2,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if withVector, ok := requestBody["with_vector"].(bool); !ok || !withVector {
		t.Fatalf("with_vector = %#v, want true", requestBody["with_vector"])
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != qdrant.StringPointID("p1") || got[0].Score < 0.91 {
		t.Errorf("first result = %+v", got[0])
	}
	if got[0].Payload["k"] != "v1" {
		t.Errorf("payload = %v", got[0].Payload)
	}
	if len(got[0].Vector) != 4 || got[0].Vector[0] != 1 || got[0].Vector[1] != 0 {
		t.Errorf("vector = %v, want populated dense vector", got[0].Vector)
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

	c := mustNewClient(t, srv.URL)
	err := c.Upsert(t.Context(), "col", []qdrant.Point{
		{ID: qdrant.StringPointID("1"), Vector: []float32{0, 1}, Payload: map[string]any{"kind": "alpha"}},
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

func TestUpsertPreservesStringAndUint64IDJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		id   qdrant.PointID
		want string
	}{
		{name: "string", id: qdrant.StringPointID("9007199254740993"), want: `"9007199254740993"`},
		{name: "uint64", id: qdrant.Uint64PointID(^uint64(0)), want: `18446744073709551615`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var request struct {
				Points []struct {
					ID json.RawMessage `json:"id"`
				} `json:"points"`
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("decode upsert request: %v", err)
				}
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(srv.Close)

			client := mustNewClient(t, srv.URL)
			err := client.Upsert(t.Context(), "col", []qdrant.Point{{
				ID:     test.id,
				Vector: []float32{1},
			}})
			if err != nil {
				t.Fatalf("Upsert: %v", err)
			}
			if len(request.Points) != 1 {
				t.Fatalf("point count = %d, want 1", len(request.Points))
			}
			if got := string(request.Points[0].ID); got != test.want {
				t.Errorf("ID JSON = %s, want %s", got, test.want)
			}
		})
	}
}

func TestPointIDJSONRoundTripPreservesKind(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		id   qdrant.PointID
	}{
		{name: "string", id: qdrant.StringPointID("18446744073709551615")},
		{name: "uint64", id: qdrant.Uint64PointID(^uint64(0))},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			encoded, err := json.Marshal(test.id)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var decoded qdrant.PointID
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if decoded != test.id {
				t.Errorf("round trip = %#v, want %#v", decoded, test.id)
			}
		})
	}
}

func TestSearchDecodesStringAndUint64IDs(t *testing.T) {
	t.Parallel()

	const stringID = "9007199254740993"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result":[` +
			`{"id":"` + stringID + `","score":0.9,"payload":{},"vector":[1]},` +
			`{"id":18446744073709551615,"score":0.8,"payload":{},"vector":[1]}` +
			`]}`))
	}))
	t.Cleanup(srv.Close)

	client := mustNewClient(t, srv.URL)
	points, err := client.Search(t.Context(), qdrant.SearchRequest{
		Collection: "col",
		Vector:     []float32{1},
		Limit:      2,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("point count = %d, want 2", len(points))
	}
	if points[0].ID != qdrant.StringPointID(stringID) {
		t.Errorf("string ID = %q, want %q", points[0].ID, stringID)
	}
	if _, ok := points[0].ID.Uint64(); ok {
		t.Errorf("string ID %q reported as numeric", points[0].ID)
	}
	if value, ok := points[1].ID.Uint64(); !ok || value != ^uint64(0) {
		t.Errorf("numeric ID = (%d, %t), want (%d, true)", value, ok, ^uint64(0))
	}
	if got := points[1].ID.String(); got != "18446744073709551615" {
		t.Errorf("numeric ID string = %q", got)
	}
}

func TestScrollPreservesUint64CursorRequestAndResponse(t *testing.T) {
	t.Parallel()

	var request struct {
		Offset json.RawMessage `json:"offset"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode scroll request: %v", err)
		}
		_, _ = w.Write([]byte(`{"result":{"points":[{"id":18446744073709551614,"payload":{}}],` +
			`"next_page_offset":18446744073709551615}}`))
	}))
	t.Cleanup(srv.Close)

	client := mustNewClient(t, srv.URL)
	points, cursor, err := client.Scroll(t.Context(), qdrant.ScrollRequest{
		Collection: "col",
		Limit:      1,
		Cursor:     scrollCursor(qdrant.Uint64ScrollCursor(18446744073709551613)),
	})
	if err != nil {
		t.Fatalf("Scroll: %v", err)
	}
	if got := string(request.Offset); got != "18446744073709551613" {
		t.Errorf("offset JSON = %s, want 18446744073709551613", got)
	}
	if len(points) != 1 {
		t.Fatalf("point count = %d, want 1", len(points))
	}
	if value, ok := points[0].ID.Uint64(); !ok || value != 18446744073709551614 {
		t.Errorf("point ID = (%d, %t), want (18446744073709551614, true)", value, ok)
	}
	if value, ok := cursor.Uint64(); !ok || value != 18446744073709551615 {
		t.Errorf("cursor = (%d, %t), want (18446744073709551615, true)", value, ok)
	}
}

func TestStringScrollCursorRequestAndNullResponse(t *testing.T) {
	t.Parallel()

	var request struct {
		Offset json.RawMessage `json:"offset"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode scroll request: %v", err)
		}
		_, _ = w.Write([]byte(`{"result":{"points":[],"next_page_offset":null}}`))
	}))
	t.Cleanup(srv.Close)

	client := mustNewClient(t, srv.URL)
	cursorValue := qdrant.StringScrollCursor("18446744073709551615")
	_, cursor, err := client.Scroll(t.Context(), qdrant.ScrollRequest{
		Collection: "col",
		Cursor:     &cursorValue,
	})
	if err != nil {
		t.Fatalf("Scroll: %v", err)
	}
	if got := string(request.Offset); got != `"18446744073709551615"` {
		t.Errorf("offset JSON = %s, want quoted string", got)
	}
	if cursor != nil {
		t.Errorf("cursor = %v, want nil", cursor)
	}
}

func TestUpsertRejectsAnyEmptyVectorBeforeHTTP(t *testing.T) {
	t.Parallel()

	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	client := mustNewClient(t, srv.URL)
	err := client.Upsert(t.Context(), "col", []qdrant.Point{
		{ID: qdrant.StringPointID("valid"), Vector: []float32{1}},
		{ID: qdrant.StringPointID("empty")},
	})
	if !errors.Is(err, qdrant.ErrEmptyVector) {
		t.Fatalf("Upsert error = %v, want ErrEmptyVector", err)
	}
	if requests != 0 {
		t.Fatalf("HTTP requests = %d, want 0", requests)
	}
}

func TestSearchRejectsEmptyVectorBeforeHTTP(t *testing.T) {
	t.Parallel()

	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	client := mustNewClient(t, srv.URL)
	_, err := client.Search(t.Context(), qdrant.SearchRequest{Collection: "col", Limit: 1})
	if !errors.Is(err, qdrant.ErrEmptyVector) {
		t.Fatalf("Search error = %v, want ErrEmptyVector", err)
	}
	if requests != 0 {
		t.Fatalf("HTTP requests = %d, want 0", requests)
	}
}

func TestNonSuccessStatusReturnsError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	t.Cleanup(srv.Close)

	c := mustNewClient(t, srv.URL)
	_, err := c.Search(t.Context(), qdrant.SearchRequest{
		Collection: "x",
		Vector:     []float32{1},
		Limit:      1,
	})
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
	col := qdrant.CollectionName("test_integration")
	c := mustNewClient(t, url)

	config := qdrant.CollectionConfig{Dimension: 4, Distance: qdrant.Distances.COSINE}
	if err := c.EnsureCollectionExists(ctx, col, config); err != nil {
		t.Fatalf("EnsureCollectionExists: %v", err)
	}
	if err := c.EnsureCollectionExists(ctx, col, config); err != nil {
		t.Fatalf("EnsureCollectionExists (idempotent): %v", err)
	}

	idA := qdrant.StringPointID("00000000-0000-0000-0000-000000000001")
	idB := qdrant.StringPointID("00000000-0000-0000-0000-000000000002")
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

func TestParseEndpointRejectsInvalidBaseURL(t *testing.T) {
	t.Parallel()

	for _, baseURL := range []string{":://bad", "qdrant.local:6333", "ftp://qdrant.local"} {
		if endpoint, err := qdrant.ParseEndpoint(baseURL); err == nil {
			t.Errorf("ParseEndpoint(%q) = (%v, nil), want error", baseURL, endpoint)
		}
	}
}

func TestEndpointZeroValueString(t *testing.T) {
	t.Parallel()

	var endpoint qdrant.Endpoint
	if got := endpoint.String(); got != "" {
		t.Errorf("Endpoint{}.String() = %q, want empty string", got)
	}
}

func TestDistanceZeroValueIsCosine(t *testing.T) {
	t.Parallel()

	var distance qdrant.Distance
	if distance != qdrant.Distances.COSINE || !distance.IsValid() {
		t.Fatalf("zero Distance = %q, want valid Cosine", distance)
	}
	got, err := json.Marshal(distance)
	if err != nil {
		t.Fatalf("Marshal zero Distance: %v", err)
	}
	if string(got) != `"Cosine"` {
		t.Errorf("Marshal zero Distance = %s, want %q", got, `"Cosine"`)
	}
}

func TestCollectionNameEscapedExactlyOnce(t *testing.T) {
	t.Parallel()

	const name = qdrant.CollectionName("percent%2F space")
	var requestURI string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestURI = r.RequestURI
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	client := mustNewClient(t, srv.URL+"/qdrant%20api/")
	if err := client.Upsert(t.Context(), name, nil); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	const want = "/qdrant%20api/collections/percent%252F%20space/points"
	if requestURI != want {
		t.Errorf("RequestURI = %q, want %q", requestURI, want)
	}
}

func TestParseCollectionNameUsesQdrantCreateGrammar(t *testing.T) {
	t.Parallel()

	for _, valid := range []string{"collection", "space and % percent", strings.Repeat("a", 255)} {
		if _, err := qdrant.ParseCollectionName(valid); err != nil {
			t.Errorf("ParseCollectionName(%q): %v", valid, err)
		}
	}
	for _, invalid := range []string{"", strings.Repeat("a", 256), "no/path", `no\\path`, "no?path", "no\x00path", "no\x1fpath"} {
		if _, err := qdrant.ParseCollectionName(invalid); err == nil {
			t.Errorf("ParseCollectionName(%q) succeeded, want error", invalid)
		}
	}
}

func mustNewClient(t *testing.T, baseURL string) *qdrant.Client {
	t.Helper()
	endpoint, err := qdrant.ParseEndpoint(baseURL)
	if err != nil {
		t.Fatalf("ParseEndpoint(%q): %v", baseURL, err)
	}
	return qdrant.NewClientWithZHTTP(endpoint, zhttp.NewClient())
}

func scrollCursor(cursor qdrant.ScrollCursor) *qdrant.ScrollCursor { return &cursor }
