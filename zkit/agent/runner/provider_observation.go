package runner

import "github.com/zarldev/zarlmono/zkit/ai/llm"

// ProviderAttemptObservation retains evidence for one invoked, settled provider
// attempt. Attempt is one-based within its Run, independent of loop iterations.
// Usage and both sidecars are the latest supplied snapshots, never chunk sums.
// RateCard is the host-selected basis frozen before dispatch, not a price estimate.
// Each is independently optional: nil means absent, not known zero. Values do
// not include errors, native continuation payloads, or descendant observations.
// A value copy borrows nested pointers; Clone makes a deep-owned snapshot.
type ProviderAttemptObservation struct {
	Attempt      int
	Usage        *llm.Usage
	PricingUsage *llm.PricingUsage
	Execution    *llm.ExecutionObservation
	RateCard     *llm.RateCard
}

// Clone returns a deep-owned snapshot with independent usage, sidecar pointers,
// slice backing storage (including cache-write rates) and execution identifier
// storage. It preserves absence
// and present unknown or known-zero evidence without inferring legacy presence.
func (o ProviderAttemptObservation) Clone() ProviderAttemptObservation {
	if o.Usage != nil {
		usage := *o.Usage
		o.Usage = &usage
	}
	if o.PricingUsage != nil {
		usage := o.PricingUsage.Clone()
		o.PricingUsage = &usage
	}
	if o.Execution != nil {
		execution := o.Execution.Clone()
		o.Execution = &execution
	}
	if o.RateCard != nil {
		card := o.RateCard.Clone()
		o.RateCard = &card
	}
	return o
}

func cloneProviderObservations(observations []ProviderAttemptObservation) []ProviderAttemptObservation {
	if observations == nil {
		return nil
	}
	clones := make([]ProviderAttemptObservation, len(observations))
	for i, observation := range observations {
		clones[i] = observation.Clone()
	}
	return clones
}
