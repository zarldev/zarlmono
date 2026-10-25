package spawn

import (
	"context"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
)

// preparation is group-owned work before a child receipt exists. Its slot is
// transferred atomically to the child; the caller still joins preparation.
type preparation struct {
	parent   taskscope.ID
	cancel   context.CancelCauseFunc
	done     chan struct{}
	promoted bool
}

func (g *Group) reservePreparation(ctx context.Context) (context.Context, uint64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil, 0, ErrGroupClosed
	}
	if err := context.Cause(ctx); err != nil {
		return nil, 0, fmt.Errorf("prepare agent task: %w", err)
	}
	parentID := taskscope.IDFrom(ctx)
	if parent, bound := g.parents[parentID]; bound && parent.sealed {
		return nil, 0, fmt.Errorf("%w: %q", ErrParentClosed, parentID)
	}
	if g.maxConcurrent > 0 && g.running >= g.maxConcurrent {
		return nil, 0, fmt.Errorf("%w (%d); await or stop a running task before spawning another", ErrMaxConcurrent, g.maxConcurrent)
	}
	preparationCtx, cancel := context.WithCancelCause(ctx)
	g.nextPreparation++
	id := g.nextPreparation
	g.preparations[id] = preparation{parent: parentID, cancel: cancel, done: make(chan struct{})}
	g.running++
	g.wg.Add(1)
	g.signalLocked(parentID)
	return preparationCtx, id, nil
}

func (g *Group) releasePreparation(id uint64) {
	g.mu.Lock()
	p := g.preparations[id]
	g.mu.Unlock()
	// Never invoke cancellation while holding the owner lock.
	p.cancel(context.Canceled)
	g.mu.Lock()
	if !p.promoted {
		g.running--
	}
	delete(g.preparations, id)
	close(p.done)
	g.signalLocked(p.parent)
	g.mu.Unlock()
	g.wg.Done()
}

func (g *Group) preparingLocked(parentID taskscope.ID) bool {
	for _, p := range g.preparations {
		if p.parent == parentID {
			return true
		}
	}
	return false
}
