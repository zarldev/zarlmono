package main

import (
	"context"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// ToolCheckEndpoint is the single tool the agent calls.
const ToolCheckEndpoint tools.ToolName = "check_endpoint"

type checkEndpointArgs struct {
	Name string `json:"name" doc:"The endpoint to check (api, db, cache)."`
}

func newCheckEndpointTool(farm *ServerFarm) tools.Tool {
	return tools.NewTyped(
		tools.ToolSpec{
			Name:        ToolCheckEndpoint,
			Description: "Check the health of an endpoint. Returns healthy, degraded, down, or transient.",
			Parameters:  tools.SchemaFor[checkEndpointArgs](),
		},
		func(_ context.Context, args checkEndpointArgs) (string, error) {
			status := farm.Check(args.Name)
			switch status {
			case StatusHealthy:
				return fmt.Sprintf("%s is healthy", args.Name), nil
			case StatusDegraded:
				return fmt.Sprintf("%s is degraded — slow but responding", args.Name), nil
			case StatusDown:
				return "", tools.Validation(ToolCheckEndpoint.String(), fmt.Sprintf("%s is down", args.Name))
			case StatusTransient:
				// The RetryGuardrail intercepts this and re-dispatches.
				// The farm auto-promotes transient→healthy on check, so the retry succeeds.
				return "", tools.Transient(ToolCheckEndpoint.String(), fmt.Errorf("%s: connection refused (transient)", args.Name))
			default:
				return "", tools.Validation(ToolCheckEndpoint.String(), fmt.Sprintf("%s: unknown status %q", args.Name, status))
			}
		},
	)
}
