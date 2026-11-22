package engine

import (
	"bytes"
	"context"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm/backends"
	"github.com/zarldev/zarlmono/zkit/db"
)

// recordedTurnCapture pairs sequential depth-zero invocations with their actual
// prepared request receipts. All mutable state belongs to buffer.mu. A prepared
// request that never invokes a provider has no settlement and no accounting row.
type recordedTurnCapture struct {
	buffer          *sessionHistoryBuffer
	provider, model string
	receipt         string
	firstReceipt    string
}

// Embedding forwards every other event unchanged. Child settlements share the
// operation anchor but never consume the current root request receipt.
type recordedAccountingSink struct {
	runner.EventSink
	capture         *recordedTurnCapture
	provider, model string
}

func (s recordedAccountingSink) OnProviderAttemptSettled(ctx context.Context, event runner.ProviderAttemptSettled) {
	if event.Depth == 0 {
		s.capture.settle(event)
	} else {
		s.capture.settleChild(event, s.provider, s.model)
	}
	// Capture, estimate and serialize before a potentially mutating/blocking sink.
	s.EventSink.OnProviderAttemptSettled(ctx, event)
}

func (c *recordedTurnCapture) settle(event runner.ProviderAttemptSettled) {
	c.buffer.mu.Lock()
	defer c.buffer.mu.Unlock()
	if c.receipt == "" {
		return
	}
	record := RecordedProviderAccounting{
		RequestBatchID: c.receipt, TaskID: string(event.TaskID), Attempt: event.Attempt,
		Provider: c.provider, Model: c.model,
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
	data, err := EncodeRecordedProviderAccounting(record)
	c.receipt = ""
	if err != nil {
		// EventSink cannot return errors. Retain the safe codec error at the
		// existing owner and reject full snapshots rather than silently losing it.
		c.buffer.accountingErr = fmt.Errorf("encode recorded provider accounting: %w", err)
		return
	}
	c.buffer.accounting = append(c.buffer.accounting, db.SessionProviderAccounting{
		RequestBatchID: record.RequestBatchID, Version: 1, PayloadJSON: data,
	})
}

// RecordedHistoryWithProviderAccounting snapshots history and settled accounting
// together under their owner lock. Returned bytes are independently owned and
// remain unchanged across persistence retries. No estimates are recomputed.
func (l *LiveRunner) RecordedHistoryWithProviderAccounting(sessionID string) ([]db.SessionHistoryBatch, []db.SessionProviderAccounting, error) {
	buffer := l.historyBuffer(sessionID)
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	if buffer.accountingErr != nil {
		return nil, nil, buffer.accountingErr
	}
	history := make([]db.SessionHistoryBatch, len(buffer.pending))
	for i, batch := range buffer.pending {
		history[i] = batch.Clone()
	}
	accounting := make([]db.SessionProviderAccounting, len(buffer.accounting))
	for i, row := range buffer.accounting {
		accounting[i] = row.Clone()
	}
	return history, accounting, nil
}

// AcknowledgeRecordedProviderAccounting releases only the exact immutable rows
// included in a successful atomic full save. Stale/repeated acknowledgments never
// release later settlements, even when task IDs and per-Run attempts repeat.
func (l *LiveRunner) AcknowledgeRecordedProviderAccounting(sessionID string, rows []db.SessionProviderAccounting) {
	buffer := l.historyBuffer(sessionID)
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	buffer.acknowledgeProviderAccountingLocked(rows)
}

func (buffer *sessionHistoryBuffer) acknowledgeProviderAccountingLocked(rows []db.SessionProviderAccounting) {
	included := make(map[string]db.SessionProviderAccounting, len(rows))
	for _, row := range rows {
		included[row.RequestBatchID] = row
	}
	pending := buffer.accounting[:0]
	for _, row := range buffer.accounting {
		committed, ok := included[row.RequestBatchID]
		if !ok || committed.Version != row.Version || !bytes.Equal(committed.PayloadJSON, row.PayloadJSON) {
			pending = append(pending, row)
		}
	}
	clear(buffer.accounting[len(pending):])
	buffer.accounting = pending
}

// ReadRecordedProviderAccounting reads one durable request receipt's evidence.
// It preserves storage/codec error identities and never selects rates, reprices,
// or infers a zero charge from a prepared but unsettled request.
func ReadRecordedProviderAccounting(ctx context.Context, store *db.Store, sessionID, requestBatchID string) (RecordedProviderAccounting, error) {
	row, err := store.GetSessionProviderAccounting(ctx, sessionID, requestBatchID)
	if err != nil {
		return RecordedProviderAccounting{}, fmt.Errorf("read recorded provider accounting: %w", err)
	}
	record, err := DecodeRecordedProviderAccounting(row.Accounting.PayloadJSON)
	if err != nil {
		return RecordedProviderAccounting{}, err
	}
	if record.RequestBatchID != row.Accounting.RequestBatchID {
		return RecordedProviderAccounting{}, ErrInvalidRecordedProviderAccounting
	}
	return record, nil
}
