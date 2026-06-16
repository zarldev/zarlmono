package homeassistant

import (
	"context"
	"fmt"

	"github.com/zarldev/zarlmono/zarlai/service"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

// ServiceCallArgs wraps arguments for the call_service tool.
type ServiceCallArgs service.Arguments

func (a ServiceCallArgs) Domain() string {
	return service.Get[string](service.Arguments(a), "domain")
}

func (a ServiceCallArgs) Service() string {
	return service.Get[string](service.Arguments(a), "service")
}

func (a ServiceCallArgs) EntityID() string {
	return service.Get[string](service.Arguments(a), "entity_id")
}

func (a ServiceCallArgs) Data() map[string]any {
	v, _ := service.Arguments(a)["data"].(map[string]any)
	return v
}

// CallServiceTool invokes a Home Assistant service to control devices.
type CallServiceTool struct {
	client *Client
}

// NewCallServiceTool creates a CallServiceTool backed by the given client.
func NewCallServiceTool(client *Client) *CallServiceTool {
	return &CallServiceTool{client: client}
}

func (t *CallServiceTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "call_service",
		Description: "Actuate a Home Assistant device — turn lights on/off, toggle switches, set thermostats, run scenes/scripts, play media. Call this for any user command to DO something in the house (\"turn on the kitchen light\", \"set the thermostat to 20\", \"lock the front door\", \"run the goodnight scene\"). If you don't know the exact entity ID, call list_entities first — never guess IDs. Common combos: light→turn_on/turn_off/toggle, switch→turn_on/turn_off, climate→set_temperature, media_player→media_play/media_pause/media_stop/volume_set, cover→open_cover/close_cover, scene→turn_on, script→turn_on. NEVER use this for Spotify content (search, play track / album / playlist, queue, skip, pause Spotify) — call the spotify_* tools instead. media_player.play_media with a Spotify URI plays the track in isolation with no album continuation; spotify_play handles context properly.",
		Parameters: service.Parameters{
			{Name: "domain", Type: service.ParamString, Description: "Entity domain — the prefix before the dot in the entity ID (light, switch, climate, media_player, cover, scene, script, lock, fan).", Required: true},
			{Name: "service", Type: service.ParamString, Description: "Service to invoke on that domain (turn_on, turn_off, toggle, set_temperature, play_media, open_cover, lock, unlock, …).", Required: true},
			{Name: "entity_id", Type: service.ParamString, Description: "Full entity ID to target (e.g. \"light.kitchen\", \"switch.kettle\"). Must come from list_entities, not made up.", Required: true},
		}.ToJSONSchema(),
	}
}

func (t *CallServiceTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	a := ServiceCallArgs(call.Arguments)
	domain := a.Domain()
	svc := a.Service()
	entityID := a.EntityID()

	if domain == "" || svc == "" || entityID == "" {
		return tools.Failure(call.ID, tools.Validation("call_service", "domain, service, and entity_id are required")), nil
	}

	if err := t.client.CallService(ctx, domain, svc, entityID, a.Data()); err != nil {
		return tools.Failure(call.ID, tools.Transient("call_service", fmt.Errorf("call service: %w", err))), nil
	}

	return tools.Success(call.ID, fmt.Sprintf("Called %s.%s on %s", domain, svc, entityID)), nil
}
