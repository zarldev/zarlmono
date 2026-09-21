package spawn

import "github.com/zarldev/zarlmono/zkit/agent/taskscope"

// OutstandingFor returns only a parent's running or unread direct children.
// Unlike Outstanding, it is safe for a shared group's per-run explicit fallback.
func (g *Group) OutstandingFor(parentID taskscope.ID) []TaskSnapshot {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var snapshots []TaskSnapshot
	for _, id := range g.order {
		t := g.tasks[id]
		if t.parent == parentID && (t.snapshot.State == AgentTaskStates.RUNNING || !t.snapshot.Observed) {
			snapshots = append(snapshots, t.snapshot)
		}
	}
	return snapshots
}

// Finish atomically seals an idle parent against late child admission. It does
// not cancel work or perform cleanup: Close still stops/joins callbacks and
// descendants at the owning run boundary. False means the parent has more work.
func (g *Group) Finish(parentID taskscope.ID) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	parent, bound := g.parents[parentID]
	if !bound || parent.sealed {
		return true
	}
	for _, id := range g.order {
		t := g.tasks[id]
		if t.parent == parentID && (t.snapshot.State == AgentTaskStates.RUNNING || !t.snapshot.Admitted) {
			return false
		}
	}
	parent.sealed = true
	g.parents[parentID] = parent
	g.signalLocked(parentID)
	return true
}
