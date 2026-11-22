package engine

import (
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
)

// HeadlessCompactionObservation is an owned snapshot of one auxiliary compactor
// invocation, not a main provider attempt. Provider and Model are the compactor's
// configured target; empty labels remain unknown. Nil sidecars mean absent
// evidence, distinct from present unknown fields or reported zero quantities.
// No root identity, rate card or estimate is inferred for this invocation.
type HeadlessCompactionObservation struct {
	Provider               string                `json:"provider"`
	Model                  string                `json:"model"`
	Duration               time.Duration         `json:"duration"`
	LegacyUsage            *HeadlessUsage        `json:"legacy_usage"`
	NormalizedPricingUsage *HeadlessPricingUsage `json:"normalized_pricing_usage"`
	Execution              *HeadlessExecution    `json:"execution"`
}

func projectCompactionObservations(observations []compact.Inference) []HeadlessCompactionObservation {
	if observations == nil {
		return nil
	}
	projected := make([]HeadlessCompactionObservation, len(observations))
	for i, observation := range observations {
		projected[i] = HeadlessCompactionObservation{
			Provider: observation.Provider, Model: observation.Model, Duration: observation.Duration,
			LegacyUsage:            projectLegacyUsage(observation.Usage),
			NormalizedPricingUsage: projectPricingUsage(observation.PricingUsage),
			Execution:              projectExecution(observation.Execution),
		}
	}
	return projected
}
