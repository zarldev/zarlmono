package sensor

import (
	"context"
	"encoding/json"
	"fmt"

	zsensor "github.com/zarldev/zarlmono/zkit/agent/sensor"

	"github.com/zarldev/zarlmono/zkit/mcp"
)

// McpNotificationSubscriber is the subset of *mcp.Client that
// McpNotificationSensor needs. Consumer-side so tests can drop a fake
// without spinning up a subprocess transport.
type McpNotificationSubscriber interface {
	Subscribe(method string, handler mcp.NotificationHandler) func()
}

// McpNotificationSensor is a Reactive sensor that fires whenever a specific
// MCP provider pushes a JSON-RPC notification with the given method. The
// method shape is provider-specific ("notifications/resources/updated",
// "notifications/tools/list_changed", custom extensions) — the sensor doesn't
// interpret it, just surfaces that a push arrived plus its params.
type McpNotificationSensor struct {
	key      string
	provider string // for logging / Detail only
	method   string
	sub      McpNotificationSubscriber

	cancel func()
	done   chan struct{}
}

// NewMcpNotificationSensor creates a sensor wrapping a provider-specific
// MCP notification subscription. provider is the provider label (persisted
// alongside the proposal); method is the JSON-RPC notification method.
func NewMcpNotificationSensor(key, provider, method string, sub McpNotificationSubscriber) *McpNotificationSensor {
	return &McpNotificationSensor{key: key, provider: provider, method: method, sub: sub}
}

// Key returns the stable identifier used by the Runner.
func (s *McpNotificationSensor) Key() string { return s.key }

// Start subscribes to the notification method and blocks until ctx ends or
// Stop is called. Every push becomes an Observation — Value is the raw JSON
// params (compact), Detail carries the provider + method for LLM context.
// The Runner applies change-detection, so a sequence of identical params
// collapses to a single notification.
func (s *McpNotificationSensor) Start(ctx context.Context, emit func(zsensor.Observation)) error {
	done := make(chan struct{})
	s.done = done
	s.cancel = s.sub.Subscribe(s.method, func(params json.RawMessage) {
		emit(zsensor.Observation{
			Value:  compactJSON(params),
			Detail: fmt.Sprintf("%s %s: %s", s.provider, s.method, compactJSON(params)),
		})
	})
	select {
	case <-ctx.Done():
	case <-done:
	}
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	return nil
}

// Stop releases the subscription and unblocks Start.
func (s *McpNotificationSensor) Stop() {
	if s.done != nil {
		select {
		case <-s.done:
		default:
			close(s.done)
		}
	}
}

// compactJSON strips insignificant whitespace so Observation.Value is a
// stable string-compare key for change detection. Invalid JSON (or empty)
// passes through unchanged — better to emit the raw bytes than to swallow
// a notification that doesn't parse.
func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return string(raw)
	}
	return string(b)
}
