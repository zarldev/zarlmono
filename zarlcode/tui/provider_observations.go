package tui

import (
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
)

// ProviderAttemptDiagnostic attributes one accepted attempt to its task-start
// provider/model. Observed placements are evidence, not canonical identities or
// pricing selections. Nil sidecars mean absent; present unknown and reported
// zero values remain distinct. Reported cost is not session spend.
// Value copies borrow sidecars; UI.ProviderObservations returns owned snapshots.
type ProviderAttemptDiagnostic struct {
	TaskID          string
	Depth, Attempt  int
	Provider, Model string
	Usage           *llm.Usage
	PricingUsage    *llm.PricingUsage
	Execution       *llm.ExecutionObservation
	// RateCard is the frozen host-selected basis, not an inferred alias price.
	RateCard *llm.RateCard
	// ConfiguredEstimate is computed at acceptance only when both the card and
	// normalized usage are present. It is not reported cost or session spend.
	ConfiguredEstimate *llm.PriceEstimate
}

// ProviderObservations returns independently owned attempt diagnostics for the
// latest accepted root turn, including its accepted children, in settlement
// order. The next accepted root start resets this transient, non-persisted list.
// Like other UI snapshots, call it on the UI event-loop owner.
func (m *UI) ProviderObservations() []ProviderAttemptDiagnostic {
	observations := m.session.Run.providerObservations
	if observations == nil {
		return nil
	}
	result := make([]ProviderAttemptDiagnostic, len(observations))
	for i, observation := range observations {
		result[i] = cloneProviderAttemptDiagnostic(observation)
	}
	return result
}

func cloneProviderAttemptDiagnostic(d ProviderAttemptDiagnostic) ProviderAttemptDiagnostic {
	if d.Usage != nil {
		d.Usage = new(*d.Usage)
	}
	if d.PricingUsage != nil {
		d.PricingUsage = new(d.PricingUsage.Clone())
	}
	if d.Execution != nil {
		d.Execution = new(d.Execution.Clone())
	}
	if d.RateCard != nil {
		d.RateCard = new(d.RateCard.Clone())
	}
	if d.ConfiguredEstimate != nil {
		d.ConfiguredEstimate = new(d.ConfiguredEstimate.Clone())
	}
	return d
}

// resetProviderObservations fences any still-tracked tasks from an earlier turn
// without changing their legacy terminal spend settlement.
func (s *RunState) resetProviderObservations() {
	s.providerObservations = nil
	s.providerObservationRootID = ""
	for id, task := range s.tasks {
		task.providerObservationRetired = true
		s.tasks[id] = task
	}
}

// Attempt deduplication and depth acceptance belong to taskAccounting.
func (s *RunState) observeProviderAttempt(e teasink.ProviderAttemptSettledMsg, task taskAccounting) {
	if task.providerObservationRetired || s.providerObservationRootID == "" || (e.Depth == 0 && e.TaskID != s.providerObservationRootID) {
		return
	}
	d := cloneProviderAttemptDiagnostic(ProviderAttemptDiagnostic{
		TaskID: e.TaskID, Depth: e.Depth, Attempt: e.Attempt,
		Provider: task.provider, Model: task.model,
		Usage: e.Usage, PricingUsage: e.PricingUsage, Execution: e.Execution, RateCard: e.RateCard,
	})
	if d.RateCard != nil && d.PricingUsage != nil {
		d.ConfiguredEstimate = new(backends.EstimatePrice(*d.RateCard, *d.PricingUsage))
	}
	s.providerObservations = append(s.providerObservations, d)
}
