package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/inference"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
)

// MaxRecordedSessionAuxiliaryAccountingBytes bounds a complete auxiliary storage record.
const MaxRecordedSessionAuxiliaryAccountingBytes = 1 << 20

// ErrInvalidRecordedSessionAuxiliaryAccounting identifies invalid auxiliary evidence
// without exposing private input or underlying parser diagnostics.
var ErrInvalidRecordedSessionAuxiliaryAccounting = errors.New("invalid recorded session auxiliary accounting")

// ErrRecordedSessionAuxiliaryAccountingVersion identifies unsupported auxiliary storage versions.
var ErrRecordedSessionAuxiliaryAccountingVersion = errors.New("unsupported recorded session auxiliary accounting version")

// RecordedSessionAuxiliaryAccounting is frozen evidence for one outer auxiliary
// provider stream, not an HTTP retry or helper semantic verdict. InvocationID is
// a canonical UUIDv4; OwnerRequestBatchID anchors the turn's first recorded root
// receipt. Optional evidence distinguishes absence from present unknown values.
// Estimates and reported cost are independent, never additive charges. A value
// copy borrows optional evidence; Clone owns its mutable storage.
type RecordedSessionAuxiliaryAccounting struct {
	InvocationID        string
	OwnerRequestBatchID string
	Purpose             AuxiliaryAccountingPurpose
	StreamOutcome       inference.StreamOutcome
	Provider, Model     string
	Usage               *llm.Usage
	PricingUsage        *llm.PricingUsage
	RateCard            *llm.RateCard
	ConfiguredEstimate  *llm.PriceEstimate
	ReportedCost        *llm.ReportedCost
}

// Clone owns all optional evidence and nested slices, preserving domain cost
// Source. Only the storage projection excludes Source.
func (r RecordedSessionAuxiliaryAccounting) Clone() RecordedSessionAuxiliaryAccounting {
	evidence := r.evidenceView().Clone()
	r.Usage, r.PricingUsage, r.RateCard = evidence.Usage, evidence.PricingUsage, evidence.RateCard
	r.ConfiguredEstimate, r.ReportedCost = evidence.ConfiguredEstimate, evidence.ReportedCost
	return r
}

// evidenceView supplies only common evidence to the existing private projection
// and clone helpers. No root identity is fabricated, validated or serialized.
func (r RecordedSessionAuxiliaryAccounting) evidenceView() RecordedProviderAccounting {
	return RecordedProviderAccounting{
		Provider: r.Provider, Model: r.Model, Usage: r.Usage, PricingUsage: r.PricingUsage,
		RateCard: r.RateCard, ConfiguredEstimate: r.ConfiguredEstimate, ReportedCost: r.ReportedCost,
	}
}

// EncodeRecordedSessionAuxiliaryAccounting projects owned allowlisted v1 JSON.
// It never computes estimates, selects rates, or includes execution data.
func EncodeRecordedSessionAuxiliaryAccounting(r RecordedSessionAuxiliaryAccounting) ([]byte, error) {
	evidence := projectAccounting(r.evidenceView())
	w := auxiliaryAccountingV1{
		Version: 1, InvocationID: r.InvocationID, OwnerRequestBatchID: r.OwnerRequestBatchID,
		Purpose: r.Purpose.String(), StreamOutcome: r.StreamOutcome.String(),
		Provider: evidence.Provider, Model: evidence.Model, Usage: evidence.Usage,
		PricingUsage: evidence.PricingUsage, RateCard: evidence.RateCard,
		EstimateAvailability: evidence.EstimateAvailability,
		ConfiguredEstimate:   evidence.ConfiguredEstimate, ReportedCost: evidence.ReportedCost,
	}
	if _, err := w.record(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(w)
	if err != nil || len(data) > MaxRecordedSessionAuxiliaryAccountingBytes || !validAccountingJSON(data) {
		return nil, ErrInvalidRecordedSessionAuxiliaryAccounting
	}
	return data, nil
}

// DecodeRecordedSessionAuxiliaryAccounting reads bounded strict v1 evidence.
// Missing required scalars, unknown/duplicate keys, malformed Unicode, invalid
// numbers and noncanonical enums reject safely. No evidence is recomputed.
func DecodeRecordedSessionAuxiliaryAccounting(data []byte) (RecordedSessionAuxiliaryAccounting, error) {
	if len(data) > MaxRecordedSessionAuxiliaryAccountingBytes || !validAccountingJSON(data) {
		return RecordedSessionAuxiliaryAccounting{}, ErrInvalidRecordedSessionAuxiliaryAccounting
	}
	var w auxiliaryAccountingV1
	if json.Unmarshal(data, &w) != nil {
		return RecordedSessionAuxiliaryAccounting{}, ErrInvalidRecordedSessionAuxiliaryAccounting
	}
	return w.record()
}

type auxiliaryAccountingV1 struct {
	Version              int                 `json:"version"`
	InvocationID         string              `json:"invocation_id"`
	OwnerRequestBatchID  string              `json:"owner_request_batch_id"`
	Purpose              string              `json:"purpose"`
	StreamOutcome        string              `json:"stream_outcome"`
	Provider             string              `json:"provider"`
	Model                string              `json:"model"`
	Usage                *accountingUsage    `json:"usage"`
	PricingUsage         *accountingPricing  `json:"pricing_usage"`
	RateCard             *accountingCard     `json:"rate_card"`
	EstimateAvailability string              `json:"estimate_availability"`
	ConfiguredEstimate   *accountingEstimate `json:"configured_estimate"`
	ReportedCost         *accountingReported `json:"reported_cost"`
}

func (w *auxiliaryAccountingV1) UnmarshalJSON(data []byte) error {
	const (
		usageField        = "usage"
		pricingField      = "pricing_usage"
		cardField         = "rate_card"
		estimateField     = "configured_estimate"
		reportedField     = "reported_cost"
		availabilityField = "estimate_availability"
	)
	type plain auxiliaryAccountingV1
	return decodeAccountingObject(data, (*plain)(w),
		[]string{usageField, pricingField, cardField, estimateField, reportedField},
		"version", "invocation_id", "owner_request_batch_id", "purpose", "stream_outcome", "provider", "model",
		usageField, pricingField, cardField, availabilityField, estimateField, reportedField)
}

func (w auxiliaryAccountingV1) evidenceView() accountingV1 {
	return accountingV1{
		Provider: w.Provider, Model: w.Model, Usage: w.Usage, PricingUsage: w.PricingUsage,
		RateCard: w.RateCard, EstimateAvailability: w.EstimateAvailability,
		ConfiguredEstimate: w.ConfiguredEstimate, ReportedCost: w.ReportedCost,
	}
}

func (w auxiliaryAccountingV1) record() (RecordedSessionAuxiliaryAccounting, error) {
	if w.Version != 1 {
		return RecordedSessionAuxiliaryAccounting{}, ErrRecordedSessionAuxiliaryAccountingVersion
	}
	id, err := uuid.Parse(w.InvocationID)
	if err != nil || id == uuid.Nil || id.Version() != 4 || id.Variant() != uuid.RFC4122 || id.String() != w.InvocationID ||
		w.OwnerRequestBatchID == "" || !accountingText(w.OwnerRequestBatchID, w.Provider, w.Model) {
		return RecordedSessionAuxiliaryAccounting{}, ErrInvalidRecordedSessionAuxiliaryAccounting
	}
	purpose, err := ParseAuxiliaryAccountingPurpose(w.Purpose)
	if err != nil || !purpose.IsValid() || purpose.String() != w.Purpose {
		return RecordedSessionAuxiliaryAccounting{}, ErrInvalidRecordedSessionAuxiliaryAccounting
	}
	outcome, err := inference.ParseStreamOutcome(w.StreamOutcome)
	if err != nil || !outcome.IsValid() || outcome.String() != w.StreamOutcome {
		return RecordedSessionAuxiliaryAccounting{}, ErrInvalidRecordedSessionAuxiliaryAccounting
	}
	evidence, err := w.evidenceView().evidenceRecord()
	if err != nil {
		return RecordedSessionAuxiliaryAccounting{}, ErrInvalidRecordedSessionAuxiliaryAccounting
	}
	return RecordedSessionAuxiliaryAccounting{
		InvocationID: w.InvocationID, OwnerRequestBatchID: w.OwnerRequestBatchID,
		Purpose: purpose, StreamOutcome: outcome, Provider: evidence.Provider, Model: evidence.Model,
		Usage: evidence.Usage, PricingUsage: evidence.PricingUsage, RateCard: evidence.RateCard,
		ConfiguredEstimate: evidence.ConfiguredEstimate, ReportedCost: evidence.ReportedCost,
	}, nil
}

// ReadRecordedSessionAuxiliaryAccounting reads owned evidence and checks its
// invocation, owner and version against the database envelope and requested key.
// Database identities are preserved; codec errors never expose parser causes.
func ReadRecordedSessionAuxiliaryAccounting(ctx context.Context, store *db.Store, sessionID, invocationID string) (RecordedSessionAuxiliaryAccounting, error) {
	row, err := store.GetSessionAuxiliaryProviderAccounting(ctx, sessionID, invocationID)
	if err != nil {
		return RecordedSessionAuxiliaryAccounting{}, fmt.Errorf("read recorded session auxiliary accounting: %w", err)
	}
	r, err := DecodeRecordedSessionAuxiliaryAccounting(row.Accounting.PayloadJSON)
	if err != nil {
		return RecordedSessionAuxiliaryAccounting{}, err
	}
	if row.Accounting.Version != 1 || r.InvocationID != invocationID || r.InvocationID != row.Accounting.InvocationID ||
		r.OwnerRequestBatchID != row.Accounting.OwnerRequestBatchID {
		return RecordedSessionAuxiliaryAccounting{}, ErrInvalidRecordedSessionAuxiliaryAccounting
	}
	return r, nil
}
