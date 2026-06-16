package tui

import (
	"strconv"
	"testing"
)

// TestContentLRU_EvictsOldestNotAll proves the cache evicts a single
// least-recently-used entry on overflow rather than wiping everything,
// and that a recently-touched entry survives past an overflow.
func TestContentLRU_EvictsOldestNotAll(t *testing.T) {
	c := newContentLRU(3)
	c.gen = themeGen

	key := func(s string) contentRenderCacheKey {
		return contentRenderCacheKey{cacheKey: s, width: 80}
	}

	c.put(key("a"), []string{"a"})
	c.put(key("b"), []string{"b"})
	c.put(key("c"), []string{"c"})

	// Touch "a" so it becomes most-recently-used; "b" is now oldest.
	if _, ok := c.get(key("a")); !ok {
		t.Fatal("a should be present")
	}

	// Insert "d" — overflow evicts the LRU entry ("b"), keeping the rest.
	c.put(key("d"), []string{"d"})

	for _, k := range []string{"a", "c", "d"} {
		if _, ok := c.get(key(k)); !ok {
			t.Errorf("%q evicted but should have survived", k)
		}
	}
	if _, ok := c.get(key("b")); ok {
		t.Error("b should have been evicted as the LRU entry")
	}
	if got := c.ll.Len(); got != 3 {
		t.Errorf("cache size = %d, want 3 (max)", got)
	}
}

// TestContentLRU_StaysWithinMax confirms a large insert burst never
// exceeds the configured ceiling.
func TestContentLRU_StaysWithinMax(t *testing.T) {
	c := newContentLRU(8)
	c.gen = themeGen
	for i := range 100 {
		c.put(contentRenderCacheKey{cacheKey: strconv.Itoa(i), width: 80}, []string{"x"})
	}
	if got := c.ll.Len(); got != 8 {
		t.Fatalf("cache size = %d, want 8", got)
	}
	if got := len(c.entries); got != 8 {
		t.Fatalf("entries map size = %d, want 8", got)
	}
}
