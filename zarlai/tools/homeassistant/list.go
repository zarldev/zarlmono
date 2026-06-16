package homeassistant

import (
	"context"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zarlai/service"
	tools "github.com/zarldev/zarlmono/zkit/ai/tools"
)

// ListEntitiesArgs wraps arguments for the list_entities tool.
type ListEntitiesArgs service.Arguments

func (a ListEntitiesArgs) Domain() string {
	return service.Get[string](service.Arguments(a), "domain")
}

// ListEntitiesTool lists available Home Assistant entities with optional domain filtering.
type ListEntitiesTool struct {
	client *Client
}

// NewListEntitiesTool creates a ListEntitiesTool backed by the given client.
func NewListEntitiesTool(client *Client) *ListEntitiesTool {
	return &ListEntitiesTool{client: client}
}

func (t *ListEntitiesTool) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "list_entities",
		Description: "Discover which Home Assistant entities exist and their current states. Use BEFORE get_state or call_service whenever the user refers to a device by friendly name (\"the kitchen light\", \"the thermostat\") rather than an entity ID, or when you need to find the right entity for an action. Filter by domain to narrow results (light, switch, sensor, climate, media_player, cover, scene, script). Returns id + friendly name + current state.",
		Parameters: service.Parameters{
			{Name: "domain", Type: service.ParamString, Description: "Optional domain filter (e.g. \"light\", \"sensor\", \"switch\", \"climate\", \"media_player\"). Omit to list everything.", Required: false},
		}.ToJSONSchema(),
	}
}

func (t *ListEntitiesTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	domain := call.Arguments.String("domain", "")

	states, err := t.client.ListStates(ctx)
	if err != nil {
		return tools.Failure(call.ID, tools.Transient("list_entities", fmt.Errorf("list states: %w", err))), nil
	}

	var lines []string
	prefix := ""
	if domain != "" {
		prefix = domain + "."
	}

	for _, s := range states {
		if prefix != "" && !strings.HasPrefix(s.EntityID, prefix) {
			continue
		}
		friendlyName, _ := s.Attributes["friendly_name"].(string)
		lines = append(lines, fmt.Sprintf("- %s (%s): %s", s.EntityID, friendlyName, s.State))
	}

	if len(lines) == 0 {
		if domain != "" {
			return tools.Success(call.ID, fmt.Sprintf("No entities found for domain %q", domain)), nil
		}
		return tools.Success(call.ID, "No entities found"), nil
	}

	return tools.Success(call.ID, strings.Join(lines, "\n")), nil
}
