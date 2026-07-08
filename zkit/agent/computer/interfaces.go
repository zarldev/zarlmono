package computer

import "context"

// Observer captures the current state of a computer surface.
type Observer interface {
	// Observe returns the current surface state using the requested observation
	// detail level.
	Observe(ctx context.Context, req ObserveRequest) (Observation, error)
}

// Actor applies an action to a computer surface and returns the resulting
// observation.
type Actor interface {
	// Act applies the requested action, honoring optional When and Until
	// triggers, then returns the resulting observed surface state.
	Act(ctx context.Context, req ActionRequest) (Observation, error)
}
