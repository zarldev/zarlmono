package engine

import (
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/db"
)

// settleChild freezes an actual descendant stream before the downstream sink
// can mutate its evidence. The child identity is independent of root re-drives.
func (r *headlessRecorder) settleChild(event runner.ProviderAttemptSettled, provider, model string) {
	record := RecordedHeadlessChildProviderAccounting{
		RunID: r.id, ChildTaskID: string(event.TaskID), Attempt: event.Attempt,
		Provider: provider, Model: model,
		Usage: event.Usage, PricingUsage: event.PricingUsage, RateCard: event.RateCard,
	}
	if event.Execution != nil {
		cost := event.Execution.ReportedCost
		cost.Source = ""
		record.ReportedCost = &cost
	}
	record = record.Clone()
	if record.RateCard != nil && record.PricingUsage != nil {
		estimate := backends.EstimatePrice(*record.RateCard, *record.PricingUsage)
		record.ConfiguredEstimate = &estimate
	}
	data, err := EncodeRecordedHeadlessChildProviderAccounting(record)
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	if err != nil {
		if r.captureErr == nil {
			r.captureErr = ErrInvalidRecordedHeadlessChildProviderAccounting
		}
		return
	}
	r.pendingChildren = append(r.pendingChildren, db.HeadlessChildProviderAccounting{
		ChildTaskID: record.ChildTaskID, Attempt: record.Attempt,
		Version: 1, PayloadJSON: data,
	})
}
