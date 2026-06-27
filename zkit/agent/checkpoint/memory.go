package checkpoint

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// MemoryStore is an in-memory Store for tests, examples, and single-process
// transient runs.
type MemoryStore struct {
	mu   sync.RWMutex
	data map[ID]Checkpoint
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore { return &MemoryStore{data: map[ID]Checkpoint{}} }

// Save stores cp, filling CreatedAt when it is zero.
func (s *MemoryStore) Save(ctx context.Context, cp Checkpoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cp.ID == "" {
		return errors.New("save checkpoint: id is required")
	}
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data == nil {
		s.data = map[ID]Checkpoint{}
	}
	s.data[cp.ID] = clone(cp)
	return nil
}

// Load returns a checkpoint by id.
func (s *MemoryStore) Load(ctx context.Context, id ID) (Checkpoint, error) {
	if err := ctx.Err(); err != nil {
		return Checkpoint{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp, ok := s.data[id]
	if !ok {
		return Checkpoint{}, fmt.Errorf("load checkpoint %q: not found", id)
	}
	return clone(cp), nil
}

// Delete removes a checkpoint.
func (s *MemoryStore) Delete(ctx context.Context, id ID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, id)
	return nil
}

// List returns checkpoints for runID ordered by CreatedAt.
func (s *MemoryStore) List(ctx context.Context, runID string) ([]Checkpoint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Checkpoint{}
	for _, cp := range s.data {
		if cp.RunID == runID {
			out = append(out, clone(cp))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func clone(cp Checkpoint) Checkpoint {
	out := cp
	if cp.State != nil {
		out.State = map[string]any{}
		for k, v := range cp.State {
			out.State[k] = v
		}
	}
	return out
}
