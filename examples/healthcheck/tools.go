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

type checkEndpointTool struct{ f *ServerFarm }

func (t checkEndpointTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolCheckEndpoint,
		Description: "Check the health of an endpoint. Returns healthy, degraded, down, or transient.",
		Parameters:  tools.SchemaFor[checkEndpointArgs](),
	}
}

func (t checkEndpointTool) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	var args checkEndpointArgs
	if err := tools.DecodeArgs(call.Arguments, &args); err != nil {
		return tools.Failure(call.ID, err), nil
	}
	status := t.f.Check(args.Name)
	switch status {
	case StatusHealthy:
		return tools.Success(call.ID, fmt.Sprintf("%s is healthy", args.Name)), nil
	case StatusDegraded:
		return tools.Success(call.ID, fmt.Sprintf("%s is degraded — slow but responding", args.Name)), nil
	case StatusDown:
		return tools.Failure(call.ID, tools.Validation(ToolCheckEndpoint.String(), fmt.Sprintf("%s is down", args.Name))), nil
	case StatusTransient:
		// The RetryGuardrail intercepts this and re-dispatches.
		// The farm auto-promotes transient→healthy on check, so the retry succeeds.
		return tools.Failure(call.ID, tools.Transient(ToolCheckEndpoint.String(), fmt.Errorf("%s: connection refused (transient)", args.Name))), nil
	default:
		return tools.Failure(call.ID, tools.Validation(ToolCheckEndpoint.String(), fmt.Sprintf("%s: unknown status %q", args.Name, status))), nil
	}
}
