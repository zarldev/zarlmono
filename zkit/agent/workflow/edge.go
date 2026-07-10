package workflow

import "context"

// Route decides the next node after a node runs. Returning End stops execution.
type Route func(ctx context.Context, state State) (NodeID, error)
