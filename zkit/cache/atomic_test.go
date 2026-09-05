package cache_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zkit/cache"
)

func TestFileCacheAtomicAcrossInstances(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writer, err := cache.NewPersistentFileCache[string, string](dir)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := cache.NewPersistentFileCache[string, string](dir)
	if err != nil {
		t.Fatal(err)
	}
	values := []string{strings.Repeat("a", 64*1024), strings.Repeat("b", 128*1024)}
	if err := writer.Set(t.Context(), "entry", values[0]); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Go(func() {
		for i := range 100 {
			if err := writer.Set(t.Context(), "entry", values[i%2]); err != nil {
				errs <- err
				return
			}
		}
	})
	wg.Go(func() {
		for range 200 {
			value, err := reader.Get(t.Context(), "entry")
			if err != nil {
				errs <- err
				return
			}
			if value != values[0] && value != values[1] {
				errs <- fmt.Errorf("partial value: %d bytes", len(value))
				return
			}
		}
	})
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
