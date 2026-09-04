package engine

import (
	"context"
	"sync"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

// livePlanStore adapts code.PlanStore for the runner. It is not the UI source
// of truth; SetPlan broadcasts PlanUpdatedMsg and the Bubble Tea update loop
// writes the canonical Session.Plan. The local copy exists only so compaction
// can read the latest plan from the runner side.
type livePlanStore struct {
	sink planEmitter
	mu   sync.Mutex
	plan code.Plan
	// version increments on every SetPlan so a turn can tell whether the live
	// plan changed during its own run (vs inheriting a stale plan from earlier
	// work) before enforcing completion-time plan hygiene.
	version uint64
}

func newLivePlanStore() *livePlanStore {
	return &livePlanStore{sink: nopLiveSink{}}
}

// LivePlanStore is the runner-side structured plan store.
type LivePlanStore = livePlanStore

// NewLivePlanStore returns an empty structured plan store.
func NewLivePlanStore() *LivePlanStore { return newLivePlanStore() }

func (p *livePlanStore) SetPlan(ctx context.Context, pl code.Plan) {
	p.mu.Lock()
	p.plan = clonePlan(pl)
	p.version++
	taskID := string(taskscope.IDFrom(ctx))
	p.mu.Unlock()
	p.sink.PlanUpdated(taskID, clonePlan(pl))
}

func (p *livePlanStore) GetPlan() code.Plan {
	pl, _ := p.Snapshot()
	return pl
}

func (p *livePlanStore) Snapshot() (code.Plan, uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return clonePlan(p.plan), p.version
}

func clonePlan(pl code.Plan) code.Plan {
	out := pl
	if len(pl.Steps) > 0 {
		out.Steps = append([]code.PlanStep(nil), pl.Steps...)
	}
	return out
}
