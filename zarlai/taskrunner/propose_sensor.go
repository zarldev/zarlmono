package taskrunner

import (
	"context"
	"fmt"

	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

// SensorProposalStore persists sensor proposals. The admin panel reads from
// the same store to surface them for human review. Each Kind has its own
// constructor because the column subsets differ: poll carries tool+args+
// interval, hass_state is entity-only, mcp_notification repurposes
// tool_name/entity_id as provider/method.
type SensorProposalStore interface {
	CreatePollProposal(ctx context.Context, toolName string, args service.Arguments, intervalSeconds int, rationale string) (id string, err error)
	CreateHassStateProposal(ctx context.Context, entityID, rationale string) (id string, err error)
	CreateMcpNotificationProposal(ctx context.Context, provider, method, rationale string) (id string, err error)
}

// ProposeSensorTool lets the agent suggest that a registered tool should be
// polled periodically, with the result broadcast as a sensor observation.
// Proposals are inert until a human approves them via the admin UI (or SQL).
type ProposeSensorTool struct {
	store    SensorProposalStore
	registry *tools.Registry
}

func NewProposeSensorTool(store SensorProposalStore, registry *tools.Registry) *ProposeSensorTool {
	return &ProposeSensorTool{store: store, registry: registry}
}

func (t *ProposeSensorTool) Definition() tools.ToolSpec {
	// ToolArgs is intentionally a free-form JSON blob; we decode it from
	// the raw arguments passed to Execute rather than via the Parameters
	// surface so nested objects survive without a schema.
	return tools.ToolSpec{
		Name:        "propose_sensor",
		Description: "Propose that one of your existing tools be invoked periodically so changes are surfaced to the user without them asking. Use this when the user expresses an ongoing interest (e.g. 'keep an eye on the front door', 'let me know when the battery drops', 'tell me when there's news about X'). Pick an appropriate tool, realistic arguments, and an interval proportional to how often the value can meaningfully change (seconds for door sensors, minutes for prices, hours for weather). Proposals are inert until a human approves them.",
		Parameters: llm.SchemaFromMap(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"tool_name":        map[string]any{"type": "string"},
				"tool_args":        map[string]any{"type": "object", "description": "Arguments to pass to the tool on each poll. Shape depends on the tool.", "additionalProperties": true},
				"interval_seconds": map[string]any{"type": "integer", "minimum": 30},
				"rationale":        map[string]any{"type": "string"},
			},
			"required": []string{"tool_name", "interval_seconds", "rationale"},
		}),
	}
}

func (t *ProposeSensorTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	args := call.Arguments
	toolName := args.String("tool_name", "")
	rationale := args.String("rationale", "")

	interval := args.Int("interval_seconds", 0)

	toolArgsRaw, _ := args["tool_args"].(map[string]any)
	toolArgs := service.Arguments(toolArgsRaw)

	switch {
	case toolName == "":
		return tools.Failure(call.ID, tools.Validation("propose_sensor", "tool_name is required")), nil
	case interval < 30:
		return tools.Failure(call.ID, tools.Validation("propose_sensor", fmt.Sprintf("interval_seconds must be at least 30 (got %d)", interval))), nil
	case rationale == "":
		return tools.Failure(call.ID, tools.Validation("propose_sensor", "rationale is required")), nil
	}

	if t.registry != nil {
		if _, ok := t.registry.Tool(tools.ToolName(toolName)); !ok {
			return tools.Failure(call.ID, tools.Validation("propose_sensor", fmt.Sprintf("tool %q is not registered — pick a tool that exists in your current tool list", toolName))), nil
		}
	}

	// The agent's propose_sensor tool only creates poll-kind proposals.
	// Reactive kinds (hass_state, mcp_notification) are created by the
	// sensor subsystem itself when it detects relevant event streams.
	id, err := t.store.CreatePollProposal(ctx, toolName, toolArgs, interval, rationale)
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("propose_sensor", fmt.Errorf("create sensor proposal: %w", err))), nil
	}
	return tools.Success(call.ID, fmt.Sprintf("Sensor proposal %s queued for human approval (tool=%q, interval=%ds).", id, toolName, interval)), nil
}
