package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
)

// MaxRecordedChildProviderAccountingBytes bounds a complete child storage record.
const MaxRecordedChildProviderAccountingBytes = 1 << 20

// ErrInvalidRecordedChildProviderAccounting identifies invalid child snapshots or
// storage JSON without exposing input text or underlying parser diagnostics.
var ErrInvalidRecordedChildProviderAccounting = errors.New("invalid recorded child provider accounting")

// ErrRecordedChildProviderAccountingVersion identifies unsupported child storage versions.
var ErrRecordedChildProviderAccountingVersion = errors.New("unsupported recorded child provider accounting version")

// RecordedChildProviderAccounting is frozen evidence for one child provider
// attempt. OwnerRequestBatchID anchors the recorded turn's first root receipt,
// not a child prepared request or necessarily the exact spawning invocation.
// Optional snapshots distinguish absence from present unknown evidence. Estimates
// and reported cost are independent, never additive charges. Value copies borrow
// optional snapshots; Clone owns their mutable storage.
type RecordedChildProviderAccounting struct {
	OwnerRequestBatchID string
	ChildTaskID         string
	Attempt             int
	Provider, Model     string
	Usage               *llm.Usage
	PricingUsage        *llm.PricingUsage
	RateCard            *llm.RateCard
	ConfiguredEstimate  *llm.PriceEstimate
	ReportedCost        *llm.ReportedCost
}

// Clone returns independently owned optional evidence, including nested slices.
// It preserves domain cost Source; the storage allowlist alone excludes Source.
func (r RecordedChildProviderAccounting) Clone() RecordedChildProviderAccounting {
	return childAccountingFromEvidence(r.evidenceView().Clone())
}

// evidenceView adapts identity and common evidence for existing projection and
// validation only. It is never serialized as a root request accounting record.
func (r RecordedChildProviderAccounting) evidenceView() RecordedProviderAccounting {
	return RecordedProviderAccounting{
		RequestBatchID: r.OwnerRequestBatchID, TaskID: r.ChildTaskID, Attempt: r.Attempt,
		Provider: r.Provider, Model: r.Model, Usage: r.Usage, PricingUsage: r.PricingUsage,
		RateCard: r.RateCard, ConfiguredEstimate: r.ConfiguredEstimate, ReportedCost: r.ReportedCost,
	}
}

func childAccountingFromEvidence(r RecordedProviderAccounting) RecordedChildProviderAccounting {
	return RecordedChildProviderAccounting{
		OwnerRequestBatchID: r.RequestBatchID, ChildTaskID: r.TaskID, Attempt: r.Attempt,
		Provider: r.Provider, Model: r.Model, Usage: r.Usage, PricingUsage: r.PricingUsage,
		RateCard: r.RateCard, ConfiguredEstimate: r.ConfiguredEstimate, ReportedCost: r.ReportedCost,
	}
}

// EncodeRecordedChildProviderAccounting projects owned strict v1 JSON containing
// child-specific identity and allowlisted frozen evidence. It neither selects
// rates nor computes estimates, and excludes execution data and cost Source.
func EncodeRecordedChildProviderAccounting(r RecordedChildProviderAccounting) ([]byte, error) {
	w := projectAccounting(r.evidenceView())
	if _, err := w.record(); err != nil {
		return nil, childAccountingError(err)
	}
	data, err := json.Marshal(childAccountingWire(w))
	if err != nil || len(data) > MaxRecordedChildProviderAccountingBytes || !validAccountingJSON(data) {
		return nil, ErrInvalidRecordedChildProviderAccounting
	}
	return data, nil
}

// DecodeRecordedChildProviderAccounting reads owned, bounded strict v1 child
// evidence. Unknown, duplicate, wrong-case, invalid or missing required fields
// reject safely. No estimates or other evidence are recomputed or repaired.
func DecodeRecordedChildProviderAccounting(data []byte) (RecordedChildProviderAccounting, error) {
	if len(data) > MaxRecordedChildProviderAccountingBytes || !validAccountingJSON(data) {
		return RecordedChildProviderAccounting{}, ErrInvalidRecordedChildProviderAccounting
	}
	var w childAccountingV1
	if json.Unmarshal(data, &w) != nil {
		return RecordedChildProviderAccounting{}, ErrInvalidRecordedChildProviderAccounting
	}
	r, err := w.evidenceView().record()
	if err != nil {
		return RecordedChildProviderAccounting{}, childAccountingError(err)
	}
	return childAccountingFromEvidence(r), nil
}

func childAccountingError(err error) error {
	if errors.Is(err, ErrRecordedProviderAccountingVersion) {
		return ErrRecordedChildProviderAccountingVersion
	}
	return ErrInvalidRecordedChildProviderAccounting
}

// Child identity is deliberately distinct on the wire. The common private DTOs
// own only the evidence schema and validation shared with root accounting v1.
type childAccountingV1 struct {
	Version              int                 `json:"version"`
	OwnerRequestBatchID  string              `json:"owner_request_batch_id"`
	ChildTaskID          string              `json:"child_task_id"`
	Attempt              int                 `json:"attempt"`
	Provider             string              `json:"provider"`
	Model                string              `json:"model"`
	Usage                *accountingUsage    `json:"usage"`
	PricingUsage         *accountingPricing  `json:"pricing_usage"`
	RateCard             *accountingCard     `json:"rate_card"`
	EstimateAvailability string              `json:"estimate_availability"`
	ConfiguredEstimate   *accountingEstimate `json:"configured_estimate"`
	ReportedCost         *accountingReported `json:"reported_cost"`
}

func childAccountingWire(w accountingV1) childAccountingV1 {
	return childAccountingV1{
		Version: w.Version, OwnerRequestBatchID: w.RequestBatchID, ChildTaskID: w.TaskID, Attempt: w.Attempt,
		Provider: w.Provider, Model: w.Model, Usage: w.Usage, PricingUsage: w.PricingUsage,
		RateCard: w.RateCard, EstimateAvailability: w.EstimateAvailability,
		ConfiguredEstimate: w.ConfiguredEstimate, ReportedCost: w.ReportedCost,
	}
}

func (w childAccountingV1) evidenceView() accountingV1 {
	return accountingV1{
		Version: w.Version, RequestBatchID: w.OwnerRequestBatchID, TaskID: w.ChildTaskID, Attempt: w.Attempt,
		Provider: w.Provider, Model: w.Model, Usage: w.Usage, PricingUsage: w.PricingUsage,
		RateCard: w.RateCard, EstimateAvailability: w.EstimateAvailability,
		ConfiguredEstimate: w.ConfiguredEstimate, ReportedCost: w.ReportedCost,
	}
}

func (w *childAccountingV1) UnmarshalJSON(data []byte) error {
	type plain childAccountingV1
	// The scanner checks exact raw keys before decoding, independently of the
	// selected encoding/json implementation's case-folding behavior. Nested
	// shared evidence DTOs apply this same scanner to every object.
	return decodeAccountingObject(data, (*plain)(w),
		[]string{"usage", "pricing_usage", "rate_card", "configured_estimate", "reported_cost"},
		"version", "owner_request_batch_id", "child_task_id", "attempt", "provider", "model", "usage", "pricing_usage",
		"rate_card", "estimate_availability", "configured_estimate", "reported_cost")
}

// ReadRecordedChildProviderAccounting reads child evidence and verifies that its
// owner, task, attempt and version agree with the database envelope. Database
// absence/corruption identities are preserved; payload failures use child codec
// identities. No rates are selected and no estimates are recomputed on restore.
func ReadRecordedChildProviderAccounting(ctx context.Context, store *db.Store, sessionID, childTaskID string, attempt int) (RecordedChildProviderAccounting, error) {
	row, err := store.GetSessionChildProviderAccounting(ctx, sessionID, childTaskID, attempt)
	if err != nil {
		return RecordedChildProviderAccounting{}, fmt.Errorf("read recorded child provider accounting: %w", err)
	}
	r, err := DecodeRecordedChildProviderAccounting(row.Accounting.PayloadJSON)
	if err != nil {
		return RecordedChildProviderAccounting{}, err
	}
	if row.Accounting.Version != 1 || r.OwnerRequestBatchID != row.Accounting.OwnerRequestBatchID ||
		r.ChildTaskID != row.Accounting.ChildTaskID || r.Attempt != row.Accounting.Attempt {
		return RecordedChildProviderAccounting{}, ErrInvalidRecordedChildProviderAccounting
	}
	return r, nil
}
