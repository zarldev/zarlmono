// Package sensor runs periodic probes that emit observations on change.
// Sensors let an agent react to ambient data (weather, home state,
// metrics, MCP server notifications) without the user having to ask.
//
// Two flavors of sensor:
//
//   - Sensor: polled on an interval. Returns the new observation on
//     change, ErrNoChange when unchanged, or a wrapped error.
//   - Reactive: holds a long-lived subscription (MQTT, HA event stream,
//     MCP server-push) and emits when the source pushes.
//
// Concrete sensors live in their consuming application — zkit/agent/sensor
// holds only the runner, the interfaces, and small adapters (Func) that
// wrap a closure as a Sensor.
package sensor

import (
	"context"
	"errors"
	"time"
)

// ErrNoChange signals that the sensor polled successfully but the
// observation is unchanged since the last tick. The runner uses this
// to suppress spammy "still the same" notifications.
var ErrNoChange = errors.New("sensor: no change")

// Sensor is a periodic observer. Key is used for logging and
// change-detection — it must be stable across ticks. Interval controls
// polling cadence; values under 100ms are clamped up to 100ms so a
// misconfigured sensor can't burn the CPU. Poll returns the
// human-readable rendering of the new observation on change,
// ErrNoChange when unchanged, or a wrapped error on failure.
type Sensor interface {
	Key() string
	Interval() time.Duration
	Poll(ctx context.Context) (Observation, error)
}

// Observation is what a sensor reports on each successful tick.
type Observation struct {
	// Value is a short human-readable summary ("21°C", "garage open").
	// Used as the notification Content and for change detection
	// (string compare).
	Value string
	// Detail is optional richer context appended to the notification
	// for the LLM. Empty is fine.
	Detail string
}

// Handler is invoked when a sensor reports a changed observation. The
// runner dispatches to Handlers on a per-sensor goroutine; keep them
// quick (push to a bus, don't block on IO).
type Handler func(ctx context.Context, key string, obs Observation)

// ErrRunnerStopped is returned by registration paths called after
// Runner.Stop. Earlier shape silently appended to a stopped runner —
// the handler goroutine started, ran past the supposed shutdown,
// and consumers couldn't tell their cleanup had been bypassed.
var ErrRunnerStopped = errors.New("sensor: runner stopped — register before Start or before Stop")

// reactiveShutdownCap bounds how long Stop / Remove will wait for a
// single reactive goroutine to honour cancellation and exit. Hitting
// the cap is logged at Warn so a misbehaving reactive is visible
// without wedging shutdown.
const reactiveShutdownCap = 10 * time.Second

// pollShutdownCap bounds how long Stop waits for polling goroutines.
// Context-aware sensors should exit promptly when Stop cancels the
// runner context; this cap keeps a context-ignoring Poll from wedging
// shutdown forever.
var pollShutdownCap = 10 * time.Second
