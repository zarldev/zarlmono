package engine

import "github.com/zarldev/zarlmono/zarlcode/rewind"

// ValidateRestoreTarget checks a prebuilt route while holding exclusive
// admission, without changing context or target. Call before committing a child
// so unavailable or incompatible routes leave the source active.
func (r *RuntimeReservation) ValidateRestoreTarget(saved rewind.Target, target RunTarget) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active || r.queuedID != 0 {
		return ErrRuntimeBusy
	}
	if saved != checkpointTarget(target) || !r.owner.checkpointTargetSupported(target) {
		return rewind.ErrTarget
	}
	return nil
}
