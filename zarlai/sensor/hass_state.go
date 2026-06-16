package sensor

import (
	"context"
	"fmt"
	"strings"

	zsensor "github.com/zarldev/zarlmono/zkit/agent/sensor"

	"github.com/zarldev/zarlmono/zarlai/tools/homeassistant"
)

// HassStateSubscriber is the subset of homeassistant.EventStream that
// HassStateSensor needs. Defined consumer-side so tests can drop in a fake
// without wiring a real WebSocket.
type HassStateSubscriber interface {
	Subscribe(entityID string, handler homeassistant.StateHandler) func()
}

// HassStateSensor is a Reactive sensor that fires whenever a specific Home
// Assistant entity reports a state transition. The heavy lifting (single
// WebSocket, auth, reconnect, multiplex) lives in the shared EventStream —
// this type is a thin adapter that materializes one entity's changes as
// sensor Observations.
type HassStateSensor struct {
	key      string
	entityID string
	stream   HassStateSubscriber

	cancel func()
	done   chan struct{}
}

// NewHassStateSensor creates a sensor that fires when entityID's state
// changes. The key is reused as the sensor's Key() — pick something stable
// that survives entity renames.
func NewHassStateSensor(key, entityID string, stream HassStateSubscriber) *HassStateSensor {
	return &HassStateSensor{key: key, entityID: entityID, stream: stream}
}

// Key returns the stable identifier used by the Runner for change detection
// and by the notification store as the "sensor:..." source label.
func (s *HassStateSensor) Key() string { return s.key }

// Start registers with the shared EventStream and blocks until ctx ends.
// Observation.Value is the new state ("on", "off", "playing", "25.3"), and
// Detail carries the entity id + transition for the LLM's benefit.
func (s *HassStateSensor) Start(ctx context.Context, emit func(zsensor.Observation)) error {
	done := make(chan struct{})
	s.done = done
	s.cancel = s.stream.Subscribe(s.entityID, func(change homeassistant.StateChange) {
		emit(zsensor.Observation{
			Value:  change.NewState.State,
			Detail: summarizeChange(change),
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

// Stop releases the subscription and unblocks Start. Safe to call multiple
// times — redundant calls are no-ops.
func (s *HassStateSensor) Stop() {
	if s.done != nil {
		select {
		case <-s.done:
			// already closed
		default:
			close(s.done)
		}
	}
}

// summarizeChange renders a state_changed event as a short human-readable
// string for LLM context. Example: "binary_sensor.front_door: off → on".
func summarizeChange(c homeassistant.StateChange) string {
	from := c.OldState.State
	if from == "" {
		from = "unknown"
	}
	return fmt.Sprintf("%s: %s → %s", c.EntityID, from, c.NewState.State)
}

// ParseHassStateWatch splits a comma-separated list of entity ids into a
// clean slice, trimming whitespace and ignoring empties. Extracted so the
// env-var parsing lives with the sensor it configures.
func ParseHassStateWatch(list string) []string {
	parts := strings.Split(list, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}
