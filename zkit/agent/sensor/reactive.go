package sensor

import "context"

// Reactive is an event-driven sensor. Unlike Sensor (which the runner
// polls on an interval), a Reactive holds a long-lived subscription to
// an external source — MQTT topic, Home Assistant state_changed event,
// MCP server-to-client notification — and emits whenever that source
// pushes.
//
// Start is called once, on a goroutine, when the Runner registers the
// sensor. It should block for the lifetime of the sensor and only
// return when ctx is cancelled or an unrecoverable error occurs. The
// supplied emit function applies the Runner's change-detection (same
// string-compare as poll sensors) so the sensor does not need to
// deduplicate itself — emitting the same Value twice is free.
//
// Stop is called during shutdown, out-of-band from ctx cancellation,
// to signal that the sensor should tear down any source-side
// subscriptions (e.g. "unsubscribe" RPC) before returning.
type Reactive interface {
	Key() string
	Start(ctx context.Context, emit func(Observation)) error
	Stop()
}
