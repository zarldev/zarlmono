package checkpoint

import "context"

// Store persists and retrieves checkpoints.
type Store interface {
	Save(ctx context.Context, cp Checkpoint) error
	Load(ctx context.Context, id ID) (Checkpoint, error)
	Delete(ctx context.Context, id ID) error
	List(ctx context.Context, runID string) ([]Checkpoint, error)
}
