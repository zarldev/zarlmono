package homeassistant

import (
	"context"
	"fmt"

	"github.com/zarldev/zarlmono/zarlai/service"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

// GetStateArgs wraps arguments for the get_state tool.
type GetStateArgs service.Arguments

func (a GetStateArgs) EntityID() string {
	return service.Get[string](service.Arguments(a), "entity_id")
}

// GetStateTool retrieves the current state of a Home Assistant entity.
type GetStateTool struct {
	client *Client
}

// NewGetStateTool creates a GetStateTool backed by the given client.
func NewGetStateTool(client *Client) *GetStateTool {
	return &GetStateTool{client: client}
}

func (t *GetStateTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "get_state",
		Description: "Read the current value of one Home Assistant entity (a sensor reading, light on/off, thermostat setpoint, media-player status, etc.). Call when the user asks about the house — \"is the kitchen light on\", \"what's the temperature inside\", \"is the dishwasher running\", \"what's the front door doing\". If you don't already know the entity ID, call list_entities first to discover it. Returns value + friendly name + unit.",
		Parameters: service.Parameters{
			{Name: "entity_id", Type: service.ParamString, Description: "Full Home Assistant entity ID in domain.name form (e.g. \"sensor.living_room_temperature\", \"light.kitchen\", \"switch.kettle\"). Do not guess — use list_entities first if unsure.", Required: true},
		}.ToJSONSchema(),
	}
}

func (t *GetStateTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	a := GetStateArgs(call.Arguments)
	entityID := a.EntityID()
	if entityID == "" {
		return tools.Failure(call.ID, tools.Validation("get_state", "entity_id is required")), nil
	}

	state, err := t.client.GetState(ctx, entityID)
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("get_state", fmt.Errorf("get state: %w", err))), nil
	}

	friendlyName, _ := state.Attributes["friendly_name"].(string)
	unit, _ := state.Attributes["unit_of_measurement"].(string)

	content := fmt.Sprintf("%s (%s): %s", state.EntityID, friendlyName, state.State)
	if unit != "" {
		content += " " + unit
	}

	return tools.Success(call.ID, content), nil
}
