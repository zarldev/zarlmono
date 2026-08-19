package docstore

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// MemoryStore stores independent document snapshots in process memory.
type MemoryStore[T Value[T]] struct {
	mu      sync.RWMutex
	records map[ID]Record[T]
}

// NewMemoryStore returns a ready in-memory document store.
func NewMemoryStore[T Value[T]]() *MemoryStore[T] {
	return &MemoryStore[T]{records: make(map[ID]Record[T])}
}

// Create stores record when its identity is not already present.
func (s *MemoryStore[T]) Create(ctx context.Context, record Record[T]) (Record[T], error) {
	if err := ensureContext(ctx); err != nil {
		return Record[T]{}, err
	}
	if record.ID == "" {
		return Record[T]{}, ErrInvalidRecord
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.records[record.ID]; exists {
		return Record[T]{}, fmt.Errorf("create %q: %w", record.ID, ErrConflict)
	}
	record = recordClone(record)
	s.records[record.ID] = record
	return recordClone(record), nil
}

// Read returns an independent snapshot for id.
func (s *MemoryStore[T]) Read(ctx context.Context, id ID) (Record[T], error) {
	if err := ensureContext(ctx); err != nil {
		return Record[T]{}, err
	}

	s.mu.RLock()
	record, exists := s.records[id]
	s.mu.RUnlock()
	if !exists {
		return Record[T]{}, fmt.Errorf("read %q: %w", id, ErrNotFound)
	}
	return recordClone(record), nil
}

// Replace atomically replaces the value for an existing record.
func (s *MemoryStore[T]) Replace(ctx context.Context, record Record[T]) (Record[T], error) {
	if err := ensureContext(ctx); err != nil {
		return Record[T]{}, err
	}
	if record.ID == "" {
		return Record[T]{}, ErrInvalidRecord
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.records[record.ID]; !exists {
		return Record[T]{}, fmt.Errorf("replace %q: %w", record.ID, ErrNotFound)
	}
	record = recordClone(record)
	s.records[record.ID] = record
	return recordClone(record), nil
}

// Put creates or replaces record and returns the stored snapshot.
func (s *MemoryStore[T]) Put(ctx context.Context, record Record[T]) (Record[T], error) {
	if err := ensureContext(ctx); err != nil {
		return Record[T]{}, err
	}
	if record.ID == "" {
		return Record[T]{}, ErrInvalidRecord
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	record = recordClone(record)
	s.records[record.ID] = record
	return recordClone(record), nil
}

// Delete removes id.
func (s *MemoryStore[T]) Delete(ctx context.Context, id ID) error {
	if err := ensureContext(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.records[id]; !exists {
		return fmt.Errorf("delete %q: %w", id, ErrNotFound)
	}
	delete(s.records, id)
	return nil
}

// List returns ID-ordered independent snapshots within page.
func (s *MemoryStore[T]) List(ctx context.Context, page Page) ([]Record[T], error) {
	if err := ensureContext(ctx); err != nil {
		return nil, err
	}
	if !page.Valid() {
		return nil, ErrInvalidRecord
	}

	s.mu.RLock()
	ids := make([]ID, 0, len(s.records))
	for id := range s.records {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	start := min(page.Offset, len(ids))
	end := len(ids)
	if page.Limit > 0 {
		end = min(start+page.Limit, end)
	}
	records := make([]Record[T], 0, end-start)
	for _, id := range ids[start:end] {
		records = append(records, recordClone(s.records[id]))
	}
	s.mu.RUnlock()
	return records, nil
}

// Count reports the number of records.
func (s *MemoryStore[T]) Count(ctx context.Context) (int, error) {
	if err := ensureContext(ctx); err != nil {
		return 0, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.records), nil
}
