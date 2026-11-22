package engine

import (
	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/db"
)

// settleAuxiliary belongs to the entire run, including descendant helpers that
// settle after root termination. It never borrows the root MAIN attempt lease.
func (r *headlessRecorder) settleAuxiliary(invocationID string, purpose AuxiliaryAccountingPurpose, target auxiliaryTarget, observation inference.Observation) {
	record := RecordedHeadlessAuxiliaryAccounting{
		RunID: r.id, InvocationID: invocationID, Purpose: purpose, StreamOutcome: observation.Outcome,
		Provider: target.providerLabel, Model: target.model,
		Usage: observation.Usage, PricingUsage: observation.PricingUsage, RateCard: target.rateCard,
	}
	if observation.Execution != nil {
		cost := observation.Execution.ReportedCost
		cost.Source = ""
		record.ReportedCost = &cost
	}
	record = record.Clone()
	if record.RateCard != nil && record.PricingUsage != nil {
		estimate := backends.EstimatePrice(*record.RateCard, *record.PricingUsage)
		record.ConfiguredEstimate = &estimate
	}
	data, err := EncodeRecordedHeadlessAuxiliaryAccounting(record)
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if err != nil {
		if r.captureErr == nil {
			// Keep only the safe semantic identity, never provider/parser data.
			r.captureErr = ErrInvalidRecordedHeadlessAuxiliaryAccounting
		}
		return
	}
	r.pendingAuxiliaries = append(r.pendingAuxiliaries, db.HeadlessAuxiliaryProviderAccounting{
		InvocationID: invocationID, Version: 1, PayloadJSON: data,
	})
}
