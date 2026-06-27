package hitl

import "time"

// Decision is a human or policy decision for a Request.
type Decision string

const (
	// DecisionApprove allows the action to proceed.
	DecisionApprove Decision = "approve"
	// DecisionDeny blocks the action.
	DecisionDeny Decision = "deny"
	// DecisionEdit asks the agent to proceed with reviewer-supplied changes.
	DecisionEdit Decision = "edit"
)

// Review records the decision for a Request.
type Review struct {
	RequestID RequestID      `json:"request_id"`
	Decision  Decision       `json:"decision"`
	Comment   string         `json:"comment,omitempty"`
	Patch     map[string]any `json:"patch,omitempty"`
	Reviewer  string         `json:"reviewer,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}
