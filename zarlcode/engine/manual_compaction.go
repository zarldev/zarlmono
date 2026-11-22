package engine

import (
	"context"
	"errors"
	"fmt"

	agentcompact "github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/db"
)

// CompactNow performs unrecorded manual compaction under a held nonqueued
// reservation. It preserves embedding behavior without inventing a session.
func (r *RuntimeReservation) CompactNow(ctx context.Context) (ManualCompactionResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active || !r.dispatchOnly || r.queuedID != 0 {
		return ManualCompactionResult{}, ErrRuntimeBusy
	}
	l := r.owner
	target := l.RunTarget()
	window := target.Window
	if window <= 0 {
		window = LiveContextWindow
	}
	engineName := agentcompact.EngineTiered
	provider, model := target.Provider, target.Model
	if l.settings != nil {
		engineName = l.settings.CompactEngine(ctx)
		provider, model = l.settings.CompactorProvider(ctx, provider, model)
	}
	return l.context.compactNow(ctx, buildLiveCompactor(engineName, window, provider, model, l, l.ws.Root()), l.sink)
}

// CompactRecordedSession compacts a settled session under a nonqueued turn
// reservation. The caller retains the reservation through execution and the
// subsequent durable save; this call never releases it or creates a turn.
// Provider evidence remains buffered even if compaction fails or is canceled.
func (r *RuntimeReservation) CompactRecordedSession(ctx context.Context, sessionID string) (ManualCompactionResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active || !r.dispatchOnly || r.queuedID != 0 {
		return ManualCompactionResult{}, ErrRuntimeBusy
	}
	l := r.owner
	target := l.RunTarget()
	bound := auxiliaryTargetFromRun(target)
	model := target.Model
	window := target.Window
	if window <= 0 {
		window = LiveContextWindow
	}
	engineName := agentcompact.EngineTiered
	if l.settings != nil {
		engineName = l.settings.CompactEngine(ctx)
		bound, model = l.settings.compactorTarget(ctx, bound)
	}
	buffer := l.historyBuffer(sessionID)
	settle := func(invocationID string, purpose AuxiliaryAccountingPurpose, target auxiliaryTarget, observation inference.Observation) {
		record := RecordedSessionManualAccounting{
			SessionID: sessionID, InvocationID: invocationID, Purpose: purpose, StreamOutcome: observation.Outcome,
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
		data, err := EncodeRecordedSessionManualAccounting(record)
		buffer.mu.Lock()
		defer buffer.mu.Unlock()
		if err != nil {
			buffer.accountingErr = fmt.Errorf("encode recorded manual provider accounting: %w", err)
			return
		}
		buffer.manual = append(buffer.manual, db.SessionManualProviderAccounting{
			InvocationID: invocationID, Version: 1, PayloadJSON: data,
		})
	}
	compactor := buildScopedCompactor(engineName, window, bound, model, l, l.ws.Root(), inference.Scope{}, newAuxiliaryProviderFactory(settle))
	result, compactionErr := l.context.compactNow(ctx, compactor, l.sink)
	buffer.mu.Lock()
	captureErr := buffer.accountingErr
	buffer.mu.Unlock()
	return result, errors.Join(compactionErr, captureErr)
}
