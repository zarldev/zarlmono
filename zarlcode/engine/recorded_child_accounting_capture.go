package engine

import (
	"bytes"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/db"
)

func (c *recordedTurnCapture) settleChild(event runner.ProviderAttemptSettled, provider, model string) {
	c.buffer.mu.Lock()
	defer c.buffer.mu.Unlock()
	if c.firstReceipt == "" {
		c.buffer.accountingErr = fmt.Errorf("record child provider accounting anchor: %w", ErrInvalidRecordedChildProviderAccounting)
		return
	}
	record := RecordedChildProviderAccounting{
		OwnerRequestBatchID: c.firstReceipt, ChildTaskID: string(event.TaskID), Attempt: event.Attempt,
		Provider: provider, Model: model, Usage: event.Usage, PricingUsage: event.PricingUsage, RateCard: event.RateCard,
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
	data, err := EncodeRecordedChildProviderAccounting(record)
	if err != nil {
		c.buffer.accountingErr = fmt.Errorf("encode recorded child provider accounting: %w", err)
		return
	}
	c.buffer.children = append(c.buffer.children, db.SessionChildProviderAccounting{
		OwnerRequestBatchID: record.OwnerRequestBatchID, ChildTaskID: record.ChildTaskID, Attempt: record.Attempt, Version: 1, PayloadJSON: data,
	})
}

// RecordedHistoryWithAccounting atomically snapshots history and both root and
// child evidence. Returned batches and payloads are independently owned, retain
// their original serialized bytes and are never repriced. Capture errors reject
// the entire snapshot rather than allowing a partial full save.
func (l *LiveRunner) RecordedHistoryWithAccounting(sessionID string) ([]db.SessionHistoryBatch, []db.SessionProviderAccounting, []db.SessionChildProviderAccounting, error) {
	buffer := l.historyBuffer(sessionID)
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	if buffer.accountingErr != nil {
		return nil, nil, nil, buffer.accountingErr
	}
	history := make([]db.SessionHistoryBatch, len(buffer.pending))
	for i, row := range buffer.pending {
		history[i] = row.Clone()
	}
	roots := make([]db.SessionProviderAccounting, len(buffer.accounting))
	for i, row := range buffer.accounting {
		roots[i] = row.Clone()
	}
	children := make([]db.SessionChildProviderAccounting, len(buffer.children))
	for i, row := range buffer.children {
		children[i] = row.Clone()
	}
	return history, roots, children, nil
}

// AcknowledgeRecordedHistoryWithAccounting releases only history batch IDs and
// exact root/child envelopes included in a successful atomic full save. Later
// settlements and stale or changed child payloads remain pending. Legacy root
// acknowledgments never release child evidence.
func (l *LiveRunner) AcknowledgeRecordedHistoryWithAccounting(sessionID string, history []db.SessionHistoryBatch, roots []db.SessionProviderAccounting, children []db.SessionChildProviderAccounting) {
	buffer := l.historyBuffer(sessionID)
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	buffer.acknowledgeHistoryLocked(history)
	buffer.acknowledgeProviderAccountingLocked(roots)
	buffer.acknowledgeChildAccountingLocked(children)
}

func (buffer *sessionHistoryBuffer) acknowledgeChildAccountingLocked(children []db.SessionChildProviderAccounting) {
	type childKey struct {
		task    string
		attempt int
	}
	included := make(map[childKey]db.SessionChildProviderAccounting, len(children))
	for _, row := range children {
		included[childKey{row.ChildTaskID, row.Attempt}] = row
	}
	pending := buffer.children[:0]
	for _, row := range buffer.children {
		committed, ok := included[childKey{row.ChildTaskID, row.Attempt}]
		if !ok || committed.OwnerRequestBatchID != row.OwnerRequestBatchID || committed.Version != row.Version || !bytes.Equal(committed.PayloadJSON, row.PayloadJSON) {
			pending = append(pending, row)
		}
	}
	clear(buffer.children[len(pending):])
	buffer.children = pending
}
