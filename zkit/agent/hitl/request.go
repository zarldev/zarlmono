package hitl

import "time"

// RequestID identifies a human review request.
type RequestID string

// RiskLevel classifies the potential impact of a requested action.
type RiskLevel string

const (
	// RiskLow is informational or readily reversible.
	RiskLow RiskLevel = "low"
	// RiskMedium may mutate state but is bounded.
	RiskMedium RiskLevel = "medium"
	// RiskHigh may be destructive, costly, or security-sensitive.
	RiskHigh RiskLevel = "high"
)

// Request describes an action that needs human review.
type Request struct {
	ID           RequestID      `json:"id"`
	RunID        string         `json:"run_id,omitempty"`
	CheckpointID string         `json:"checkpoint_id,omitempty"`
	Action       string         `json:"action"`
	Summary      string         `json:"summary"`
	Payload      map[string]any `json:"payload,omitempty"`
	Risk         RiskLevel      `json:"risk,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
}
