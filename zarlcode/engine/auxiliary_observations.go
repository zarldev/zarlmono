package engine

import (
	"context"
	"sync"

	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/options"
)

// AuxiliaryObservation is ephemeral evidence for one judge or spawn-planner
// invocation. ResponseValid describes the helper's parsed response, not stream
// completion or downstream task success. A value copy borrows sidecars.
type AuxiliaryObservation struct {
	Kind          AuxiliaryKind
	Inference     inference.Observation
	ResponseValid bool
}

// Clone returns independently owned inference evidence.
func (o AuxiliaryObservation) Clone() AuxiliaryObservation {
	o.Inference = o.Inference.Clone()
	return o
}

// AuxiliarySnapshot retains only the latest committed observation per role in
// one application turn (the whole invocation for headless verified re-drives).
// Nil means no admitted invocation observed. It is not a ledger or spend total.
type AuxiliarySnapshot struct {
	Judge        *AuxiliaryObservation
	SpawnPlanner *AuxiliaryObservation
}

// Clone returns independent slots and inference sidecars.
func (s AuxiliarySnapshot) Clone() AuxiliarySnapshot {
	if s.Judge != nil {
		s.Judge = new(s.Judge.Clone())
	}
	if s.SpawnPlanner != nil {
		s.SpawnPlanner = new(s.SpawnPlanner.Clone())
	}
	return s
}

// WithAuxiliaryObservationSink installs a construction-only callback, separate
// from runner events and accounting. It receives owned evidence synchronously
// after a slot commits, outside the collector lock. It must support concurrent
// publishers and must not retain ctx. The default callback does nothing.
func WithAuxiliaryObservationSink(sink func(context.Context, AuxiliaryObservation)) options.Option[LiveRunner] {
	return func(l *LiveRunner) { l.auxiliaryObserved = sink }
}

func ignoreAuxiliaryObservation(context.Context, AuxiliaryObservation) {}

type auxiliaryCollector struct {
	mu      sync.Mutex
	latest  AuxiliarySnapshot
	forward func(context.Context, AuxiliaryObservation)
}

func (c *auxiliaryCollector) observe(ctx context.Context, o AuxiliaryObservation) {
	c.mu.Lock()
	owned := o.Clone()
	switch owned.Kind {
	case AuxiliaryKinds.JUDGE:
		c.latest.Judge = &owned
	case AuxiliaryKinds.SPAWNPLANNER:
		c.latest.SpawnPlanner = &owned
	}
	forwarded := owned.Clone()
	c.mu.Unlock()
	c.forward(ctx, forwarded)
}

func (c *auxiliaryCollector) snapshot() AuxiliarySnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.latest.Clone()
}
