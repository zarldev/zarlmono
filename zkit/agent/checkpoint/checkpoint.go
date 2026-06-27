package checkpoint

import "time"

// ID identifies a checkpoint.
type ID string

// Checkpoint is a serialisable snapshot of a run. State is intentionally open
// so workflow, runner, or product code can store their own JSON-shaped data.
type Checkpoint struct {
	ID        ID             `json:"id"`
	RunID     string         `json:"run_id"`
	Step      string         `json:"step,omitempty"`
	State     map[string]any `json:"state,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}
