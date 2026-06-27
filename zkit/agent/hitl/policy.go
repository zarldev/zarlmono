package hitl

import "context"

// Policy decides whether a Request can proceed automatically or needs review.
type Policy interface {
	Review(ctx context.Context, req Request) (Review, bool, error)
}

// PolicyFunc adapts a function to Policy. The bool reports whether a decision
// was made; false means the request should be surfaced to a human.
type PolicyFunc func(context.Context, Request) (Review, bool, error)

// Review calls f itself.
func (f PolicyFunc) Review(ctx context.Context, req Request) (Review, bool, error) {
	return f(ctx, req)
}

// RequireHuman is a Policy that always defers to a human reviewer.
type RequireHuman struct{}

// Review always returns decided=false.
func (RequireHuman) Review(context.Context, Request) (Review, bool, error) {
	return Review{}, false, nil
}

// ApproveLowRisk approves RiskLow requests and defers all others.
type ApproveLowRisk struct{}

// Review approves low-risk requests.
func (ApproveLowRisk) Review(_ context.Context, req Request) (Review, bool, error) {
	if req.Risk == RiskLow {
		return Review{RequestID: req.ID, Decision: DecisionApprove, Reviewer: "policy:approve_low_risk"}, true, nil
	}
	return Review{}, false, nil
}
