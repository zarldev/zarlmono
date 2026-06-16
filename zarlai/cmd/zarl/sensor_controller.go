package main

import (
	"fmt"
	"log/slog"
	"time"

	zsensor "github.com/zarldev/zarlmono/zkit/agent/sensor"
	"github.com/zarldev/zarlmono/zkit/ai/tools"

	"github.com/zarldev/zarlmono/zarlai/repository"
	"github.com/zarldev/zarlmono/zarlai/sensor"
	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zarlai/tools/homeassistant"
	"github.com/zarldev/zarlmono/zkit/mcp"
)

// McpClientResolver looks up an active MCP client by provider name. The
// ToolManager implements this — defined consumer-side here so the sensor
// controller doesn't import the transport package.
type McpClientResolver interface {
	McpClient(name string) (*mcp.Client, bool)
}

// SensorController is the runtime glue between stored proposals and the live
// sensor runner. It knows how to materialize each SensorKind, register it on
// the Runner, and compute the stable key used by notifications.
//
// Methods are safe to call from admin RPC handlers. Unknown or unsupported
// kinds return a descriptive error; callers surface that back to the operator
// who approved the proposal.
type SensorController struct {
	runner    *zsensor.Runner
	registry  *tools.Registry
	haStream  *homeassistant.EventStream // nil when HA isn't configured
	mcpLookup McpClientResolver          // nil only in tests
}

// NewSensorController wires the runtime dependencies needed to instantiate
// each sensor kind. haStream may be nil (reactive HA sensors then return a
// clear error) and mcpLookup may be nil (mcp_notification sensors error).
func NewSensorController(runner *zsensor.Runner, registry *tools.Registry, haStream *homeassistant.EventStream, mcpLookup McpClientResolver) *SensorController {
	return &SensorController{runner: runner, registry: registry, haStream: haStream, mcpLookup: mcpLookup}
}

// Runner returns the underlying zsensor.Runner so callers that register
// built-in sensors or subscribe to OnChange can do so without reaching
// through the controller for every operation.
func (c *SensorController) Runner() *zsensor.Runner { return c.runner }

// Activate turns a proposal into a live sensor. Idempotent: re-activating an
// already-running sensor is a no-op. Returns the key the sensor was
// registered under so callers can correlate with notifications.
func (c *SensorController) Activate(p repository.SensorProposal) (string, error) {
	key := sensorKeyFor(p)
	// Bail early if this proposal is already live — approve may be called
	// twice, or startup may race with a hot-approve. Either way we should
	// not double-register.
	if c.runner.IsRunning(key) {
		return key, nil
	}
	switch p.Kind {
	case repository.SensorKindPoll:
		tool, ok := c.registry.Tool(tools.ToolName(p.ToolName))
		if !ok {
			return "", fmt.Errorf("tool %q is not registered — its provider may be disabled", p.ToolName)
		}
		interval := time.Duration(p.IntervalSeconds) * time.Second
		if err := c.runner.Register(sensor.FromTool(key, tool, service.Arguments(p.ToolArgs), interval)); err != nil {
			return "", fmt.Errorf("register poll sensor %q: %w", key, err)
		}
	case repository.SensorKindHassState:
		if c.haStream == nil {
			return "", fmt.Errorf("home assistant event stream is not running — configure a home_assistant provider in /admin → Tools")
		}
		if p.EntityID == "" {
			return "", fmt.Errorf("hass_state proposal %s has empty entity_id", p.ID)
		}
		if err := c.runner.RegisterReactive(sensor.NewHassStateSensor(key, p.EntityID, c.haStream)); err != nil {
			return "", fmt.Errorf("register hass_state sensor %q: %w", key, err)
		}
	case repository.SensorKindMcpNotification:
		if c.mcpLookup == nil {
			return "", fmt.Errorf("mcp client lookup is not configured — cannot activate mcp_notification sensors")
		}
		provider := p.ToolName // repurposed column — see SensorKindMcpNotification docs
		method := p.EntityID   // repurposed column — the JSON-RPC method
		if provider == "" || method == "" {
			return "", fmt.Errorf("mcp_notification proposal %s missing provider or method", p.ID)
		}
		client, ok := c.mcpLookup.McpClient(provider)
		if !ok {
			return "", fmt.Errorf("mcp provider %q is not loaded — check tool_providers config", provider)
		}
		if err := c.runner.RegisterReactive(sensor.NewMcpNotificationSensor(key, provider, method, client)); err != nil {
			return "", fmt.Errorf("register mcp_notification sensor %q: %w", key, err)
		}
	default:
		return "", fmt.Errorf("unknown sensor kind %q", p.Kind)
	}
	slog.Info("sensor activated", "id", p.ID, "kind", p.Kind, "key", key)
	return key, nil
}

// Deactivate removes a live sensor. Safe to call even if the sensor was
// never activated. Returns true when a live sensor was actually removed.
func (c *SensorController) Deactivate(p repository.SensorProposal) bool {
	key := sensorKeyFor(p)
	removed := c.runner.Remove(key)
	if removed {
		slog.Info("sensor deactivated", "id", p.ID, "kind", p.Kind, "key", key)
	}
	return removed
}

// ActivateAllApproved is the startup helper that restores every
// previously-approved sensor. Failures are logged and skipped so one bad
// proposal can't stop the rest from booting.
func (c *SensorController) ActivateAllApproved(proposals []repository.SensorProposal) {
	for _, p := range proposals {
		if _, err := c.Activate(p); err != nil {
			slog.Warn("approved sensor did not activate", "id", p.ID, "kind", p.Kind, "error", err)
		}
	}
}

// sensorKeyFor picks the stable, human-readable key used by the notification
// store ("sensor:<key>") and by the Runner for change-detection and removal.
// Including the kind makes notifications self-describing and prevents collisions
// between a poll sensor over tool "foo" and a reactive sensor over entity "foo".
func sensorKeyFor(p repository.SensorProposal) string {
	switch p.Kind {
	case repository.SensorKindHassState:
		return "hass_state." + p.EntityID
	case repository.SensorKindMcpNotification:
		return "mcp." + p.ToolName + "." + p.EntityID
	case repository.SensorKindPoll:
		return "agent_" + shortID(p.ID) + "_" + p.ToolName
	default:
		return "unknown_" + shortID(p.ID)
	}
}

func shortID(id string) string {
	if len(id) < 8 {
		return id
	}
	return id[:8]
}
