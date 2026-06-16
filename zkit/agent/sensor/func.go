package sensor

import (
	"context"
	"sync"
	"time"
)

// PollFunc is the shape of a sensor's data-gathering step: returns the
// next observation or a non-nil error. Returning an empty Value is
// treated as "no current observation" — the change detector will not
// emit for those ticks.
type PollFunc func(ctx context.Context) (Observation, error)

// Func adapts a plain PollFunc into a Sensor with built-in change
// detection. It is the usual entry point — most concrete sensors don't
// need their own type, just a closure that hits some source.
type Func struct {
	key      string
	interval time.Duration
	poll     PollFunc

	mu   sync.Mutex
	last string // last emitted Value — empty means "nothing emitted yet"
}

// NewFunc wraps poll as a Sensor. key must be unique across registered
// sensors; interval is clamped up to 100ms by the Runner.
func NewFunc(key string, interval time.Duration, poll PollFunc) *Func {
	return &Func{key: key, interval: interval, poll: poll}
}

// Key returns the sensor's stable identifier.
func (f *Func) Key() string { return f.key }

// Interval returns the configured polling cadence.
func (f *Func) Interval() time.Duration { return f.interval }

// Poll calls the underlying PollFunc and applies change detection: a
// Value identical to the last emitted observation returns ErrNoChange.
func (f *Func) Poll(ctx context.Context) (Observation, error) {
	obs, err := f.poll(ctx)
	if err != nil {
		return Observation{}, err
	}
	if obs.Value == "" {
		return Observation{}, ErrNoChange
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if obs.Value == f.last {
		return Observation{}, ErrNoChange
	}
	f.last = obs.Value
	return obs, nil
}
