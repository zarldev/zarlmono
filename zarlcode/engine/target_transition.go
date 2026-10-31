package engine

import (
	"context"
	"fmt"

	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/rewind"
)

// ApplyTargetSelection admits a target against retained conversation state,
// persists its selection, then publishes the target. A rejection or failed
// preference write leaves both the active target and conversation unchanged.
func (r *RuntimeReservation) ApplyTargetSelection(ctx context.Context, update TargetUpdate, preferences *prefs.Service, selection prefs.ModelSelection) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.applyTarget(update, func() error { return preferences.SetModelSelection(ctx, prefs.ScopeWorkspace, selection) })
}

func (r *RuntimeReservation) applyTarget(update TargetUpdate, persist func() error) error {
	if !r.active || !r.targetMutable {
		return ErrRuntimeBusy
	}
	if update.Window <= 0 {
		update.Window = LiveContextWindow
	}
	// Admission excludes writers. Match the context -> publication -> owner
	// ordering used by conversation restore; never publish before validation.
	r.owner.context.mu.Lock()
	defer r.owner.context.mu.Unlock()
	r.owner.publication.Lock()
	defer r.owner.publication.Unlock()
	r.owner.mu.Lock()
	current := r.owner.target
	r.owner.mu.Unlock()
	next := current
	next.Provider, next.Spec, next.Model, next.Window = update.Provider, update.Spec, update.Spec.Model, update.Window
	if r.owner.context.exact && len(r.owner.context.context) > 0 && checkpointTarget(next) != checkpointTarget(current) {
		return fmt.Errorf("%w: start a new conversation to change an exact replay target", rewind.ErrTarget)
	}
	if err := rewind.ValidateLegacyContext(r.owner.context.context, update.Spec.Name); err != nil {
		return fmt.Errorf("%w: retained conversation is incompatible with the selected route", rewind.ErrTarget)
	}
	if err := persist(); err != nil {
		return fmt.Errorf("model selection persistence: %w", err)
	}
	r.owner.mu.Lock()
	r.owner.target = next
	r.owner.mu.Unlock()
	return nil
}
