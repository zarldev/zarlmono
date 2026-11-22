package engine

import (
	"bytes"
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/db"
)

// auxiliaryTarget binds evidence to the actual construction, independently of
// the model label retained by helper diagnostics. Its optional card is owned.
type auxiliaryTarget struct {
	provider             llm.Provider
	providerLabel, model string
	rateCard             *llm.RateCard
}

func auxiliaryTargetFromRun(target RunTarget) auxiliaryTarget {
	bound := auxiliaryTarget{provider: target.Provider, providerLabel: target.Spec.Name, model: target.Model}
	if target.RateCard != nil {
		card := target.RateCard.Clone()
		bound.rateCard = &card
	}
	return bound
}

type auxiliaryProviderFactory func(AuxiliaryAccountingPurpose, auxiliaryTarget) llm.Provider

func (f auxiliaryProviderFactory) bind(purpose AuxiliaryAccountingPurpose, target auxiliaryTarget) llm.Provider {
	if f == nil {
		return target.provider
	}
	return f(purpose, target)
}

// newAuxiliaryProviderFactory binds synchronous settlement to the recording owner.
// The wrapper remains lazy and never charges a second inference account.
func newAuxiliaryProviderFactory(settle func(string, AuxiliaryAccountingPurpose, auxiliaryTarget, inference.Observation)) auxiliaryProviderFactory {
	return func(purpose AuxiliaryAccountingPurpose, target auxiliaryTarget) llm.Provider {
		return auxiliaryAccountingProvider{target: target, purpose: purpose, settle: settle}
	}
}

// auxiliaryAccountingProvider wraps only helpers whose complete capability is
// Name/Complete. Runner providers retain their original optional capabilities.
type auxiliaryAccountingProvider struct {
	target  auxiliaryTarget
	purpose AuxiliaryAccountingPurpose
	settle  func(string, AuxiliaryAccountingPurpose, auxiliaryTarget, inference.Observation)
}

var _ llm.Provider = auxiliaryAccountingProvider{}

func (p auxiliaryAccountingProvider) Name() string { return p.target.provider.Name() }

func (p auxiliaryAccountingProvider) Complete(ctx context.Context, request llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		invocationID := uuid.NewString()
		// The zero account is intentional: the existing outer helper alone admits
		// and settles charges. Observation clones before yield and completes before
		// returning into outer settlement and semantic callbacks.
		_ = (inference.Scope{}).InvokeObserved(ctx, inference.Call{}, p.target.provider, request,
			func(stream llm.CompletionStream) { stream(yield) },
			func(_ context.Context, observation inference.Observation) {
				p.settle(invocationID, p.purpose, p.target, observation)
			})
	}
}

func (c *recordedTurnCapture) settleAuxiliary(invocationID string, purpose AuxiliaryAccountingPurpose, target auxiliaryTarget, observation inference.Observation) {
	record := RecordedSessionAuxiliaryAccounting{
		InvocationID: invocationID, Purpose: purpose, StreamOutcome: observation.Outcome,
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
	c.buffer.mu.Lock()
	defer c.buffer.mu.Unlock()
	if c.firstReceipt == "" {
		c.buffer.accountingErr = fmt.Errorf("record auxiliary provider accounting anchor: %w", ErrInvalidRecordedSessionAuxiliaryAccounting)
		return
	}
	record.OwnerRequestBatchID = c.firstReceipt
	data, err := EncodeRecordedSessionAuxiliaryAccounting(record)
	if err != nil {
		c.buffer.accountingErr = fmt.Errorf("encode recorded auxiliary provider accounting: %w", err)
		return
	}
	c.buffer.auxiliaries = append(c.buffer.auxiliaries, db.SessionAuxiliaryProviderAccounting{
		InvocationID: invocationID, OwnerRequestBatchID: record.OwnerRequestBatchID, Version: 1, PayloadJSON: data,
	})
}

// RecordedHistoryWithAccountingBatch atomically snapshots history and all root,
// child and auxiliary evidence. Every returned payload is independently owned;
// retries retain exactly these bytes without repricing. Any capture error rejects
// the entire snapshot.
func (l *LiveRunner) RecordedHistoryWithAccountingBatch(sessionID string) ([]db.SessionHistoryBatch, db.SessionAccountingBatch, error) {
	buffer := l.historyBuffer(sessionID)
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	if buffer.accountingErr != nil {
		return nil, db.SessionAccountingBatch{}, buffer.accountingErr
	}
	history := make([]db.SessionHistoryBatch, len(buffer.pending))
	for i, row := range buffer.pending {
		history[i] = row.Clone()
	}
	batch := db.SessionAccountingBatch{
		Roots:       make([]db.SessionProviderAccounting, len(buffer.accounting)),
		Children:    make([]db.SessionChildProviderAccounting, len(buffer.children)),
		Auxiliaries: make([]db.SessionAuxiliaryProviderAccounting, len(buffer.auxiliaries)),
		Manual:      make([]db.SessionManualProviderAccounting, len(buffer.manual)),
	}
	for i, row := range buffer.accounting {
		batch.Roots[i] = row.Clone()
	}
	for i, row := range buffer.children {
		batch.Children[i] = row.Clone()
	}
	for i, row := range buffer.auxiliaries {
		batch.Auxiliaries[i] = row.Clone()
	}
	for i, row := range buffer.manual {
		batch.Manual[i] = row.Clone()
	}
	return history, batch, nil
}

// AcknowledgeRecordedHistoryWithAccountingBatch releases only the history and
// exact evidence included in a successful atomic full save. Call only after
// commit succeeds; late arrivals and changed or stale envelopes stay pending.
// Legacy acknowledgment APIs never release auxiliary evidence.
func (l *LiveRunner) AcknowledgeRecordedHistoryWithAccountingBatch(sessionID string, history []db.SessionHistoryBatch, accounting db.SessionAccountingBatch) {
	buffer := l.historyBuffer(sessionID)
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	buffer.acknowledgeHistoryLocked(history)
	buffer.acknowledgeProviderAccountingLocked(accounting.Roots)
	buffer.acknowledgeChildAccountingLocked(accounting.Children)
	included := make(map[string]db.SessionAuxiliaryProviderAccounting, len(accounting.Auxiliaries))
	for _, row := range accounting.Auxiliaries {
		included[row.InvocationID] = row
	}
	pending := buffer.auxiliaries[:0]
	for _, row := range buffer.auxiliaries {
		committed, ok := included[row.InvocationID]
		if !ok || committed.OwnerRequestBatchID != row.OwnerRequestBatchID || committed.Version != row.Version || !bytes.Equal(committed.PayloadJSON, row.PayloadJSON) {
			pending = append(pending, row)
		}
	}
	clear(buffer.auxiliaries[len(pending):])
	buffer.auxiliaries = pending
	manual := make(map[string]db.SessionManualProviderAccounting, len(accounting.Manual))
	for _, row := range accounting.Manual {
		manual[row.InvocationID] = row
	}
	remaining := buffer.manual[:0]
	for _, row := range buffer.manual {
		committed, ok := manual[row.InvocationID]
		if !ok || committed.Version != row.Version || !bytes.Equal(committed.PayloadJSON, row.PayloadJSON) {
			remaining = append(remaining, row)
		}
	}
	clear(buffer.manual[len(remaining):])
	buffer.manual = remaining
}
