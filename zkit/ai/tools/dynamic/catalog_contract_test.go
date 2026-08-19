package dynamic_test

import (
	"context"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/dynamic"
)

type blockingStore struct {
	mu sync.Mutex

	entries []dynamic.Entry

	upsertStarted chan struct{}
	releaseUpsert chan struct{}
}

func newBlockingStore() *blockingStore {
	return &blockingStore{
		upsertStarted: make(chan struct{}),
		releaseUpsert: make(chan struct{}),
	}
}

func (s *blockingStore) List(context.Context) ([]dynamic.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]dynamic.Entry(nil), s.entries...), nil
}

func (s *blockingStore) Upsert(ctx context.Context, entry dynamic.Entry) error {
	close(s.upsertStarted)
	select {
	case <-s.releaseUpsert:
	case <-ctx.Done():
		return ctx.Err()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for i, current := range s.entries {
		if current.Spec.Name == entry.Spec.Name {
			s.entries[i] = entry
			return nil
		}
	}
	s.entries = append(s.entries, entry)
	return nil
}

func (s *blockingStore) Delete(_ context.Context, name tools.ToolName) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, entry := range s.entries {
		if entry.Spec.Name == name {
			s.entries = append(s.entries[:i], s.entries[i+1:]...)
			break
		}
	}
	return nil
}

func TestCatalog_SerializesInterleavedAddRemove(t *testing.T) {
	store := newBlockingStore()
	catalog := dynamic.NewCatalog(store)
	entry := dynamic.Entry{
		Spec:       tools.ToolSpec{Name: "example"},
		BinaryPath: "/tmp/example",
	}

	addDone := make(chan error, 1)
	go func() {
		addDone <- catalog.AddContext(t.Context(), entry)
	}()
	<-store.upsertStarted

	removeStarted := make(chan struct{})
	removeDone := make(chan error, 1)
	go func() {
		close(removeStarted)
		removeDone <- catalog.RemoveContext(t.Context(), entry.Spec.Name)
	}()
	<-removeStarted

	select {
	case err := <-removeDone:
		t.Fatalf("RemoveContext completed while AddContext store write was blocked: %v", err)
	default:
	}

	close(store.releaseUpsert)
	if err := <-addDone; err != nil {
		t.Fatalf("AddContext: %v", err)
	}
	if err := <-removeDone; err != nil {
		t.Fatalf("RemoveContext: %v", err)
	}

	if _, ok := catalog.Get(entry.Spec.Name); ok {
		t.Fatal("catalog retains entry after serialized remove")
	}
	persisted, err := store.List(t.Context())
	if err != nil {
		t.Fatalf("store List: %v", err)
	}
	if len(persisted) != 0 {
		t.Fatalf("persistent entries = %v, want none", persisted)
	}
}
