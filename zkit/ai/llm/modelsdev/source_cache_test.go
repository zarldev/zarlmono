package modelsdev_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm/modelsdev"
	"github.com/zarldev/zarlmono/zkit/cache"
)

type countingSnapshotCache struct {
	inner cache.Cache[string, modelsdev.Snapshot]
	gets  atomic.Int64
}

func TestSourceStaleSnapshotBacksOffAfterRefreshFailure(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "offline", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	store := cache.NewMemoryCache[string, modelsdev.Snapshot]()
	stale := modelsdev.Snapshot{
		Schema: 2, FetchedAt: time.Now().Add(-time.Hour),
		Entries: map[string]map[string]modelsdev.Entry{"openai": {"gpt-test": {}}},
	}
	if err := store.Set(t.Context(), "models.dev", stale); err != nil {
		t.Fatal(err)
	}
	source := modelsdev.New(store, modelsdev.WithTTL(time.Minute), modelsdev.WithBaseURL(server.URL))
	for range 5 {
		if _, ok := source.Lookup(t.Context(), "openai", "gpt-test"); !ok {
			t.Fatal("stale snapshot was not served")
		}
	}
	// The shared HTTP client retries one refresh three times. The important
	// invariant is that the following four lookups do not start new refreshes.
	if got := requests.Load(); got != 3 {
		t.Fatalf("refresh requests = %d, want one three-attempt refresh during backoff", got)
	}
}

func (c *countingSnapshotCache) Get(ctx context.Context, key string) (modelsdev.Snapshot, error) {
	c.gets.Add(1)
	return c.inner.Get(ctx, key)
}

func (c *countingSnapshotCache) Set(ctx context.Context, key string, value modelsdev.Snapshot) error {
	return c.inner.Set(ctx, key, value)
}

func (c *countingSnapshotCache) Delete(ctx context.Context, key string) (bool, error) {
	return c.inner.Delete(ctx, key)
}

func (c *countingSnapshotCache) Clear(ctx context.Context) error { return c.inner.Clear(ctx) }

func (c *countingSnapshotCache) Len(ctx context.Context) (int, error) {
	return c.inner.Len(ctx)
}

func (c *countingSnapshotCache) Healthy(ctx context.Context) error {
	return c.inner.Healthy(ctx)
}

// Repeated metadata resolution drives every visible Ctrl+E model-picker row.
// Once Source has loaded a fresh snapshot, those lookups must stay in memory
// rather than rereading and unmarshalling the whole persistent cache per field.
func TestSourceLookupCachesSnapshotInMemory(t *testing.T) {
	store := &countingSnapshotCache{inner: cache.NewMemoryCache[string, modelsdev.Snapshot]()}
	snapshot := modelsdev.Snapshot{
		Schema:    2,
		FetchedAt: time.Now(),
		Entries: map[string]map[string]modelsdev.Entry{
			"openai": {
				"gpt-test": {Intrinsic: modelsdev.Intrinsic{ContextWindow: 128_000, SupportsTools: true}},
			},
		},
	}
	if err := store.Set(t.Context(), "models.dev", snapshot); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}
	source := modelsdev.New(store)

	for range 20 {
		if _, ok := source.Lookup(t.Context(), "openai", "gpt-test"); !ok {
			t.Fatal("provider lookup missed seeded model")
		}
		if _, ok := source.LookupIntrinsic(t.Context(), "gpt-test"); !ok {
			t.Fatal("intrinsic lookup missed seeded model")
		}
	}
	if got := store.gets.Load(); got != 1 {
		t.Fatalf("persistent cache reads = %d, want 1", got)
	}
}
