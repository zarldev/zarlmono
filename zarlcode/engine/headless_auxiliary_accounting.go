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

// MaxRecordedHeadlessAuxiliaryAccountingBytes bounds a complete auxiliary storage record.
const MaxRecordedHeadlessAuxiliaryAccountingBytes = 1 << 20

// ErrInvalidRecordedHeadlessAuxiliaryAccounting identifies invalid auxiliary evidence
// without exposing private input or underlying parser diagnostics.
var ErrInvalidRecordedHeadlessAuxiliaryAccounting = errors.New("invalid recorded headless auxiliary accounting")

// ErrRecordedHeadlessAuxiliaryAccountingVersion identifies unsupported auxiliary storage versions.
var ErrRecordedHeadlessAuxiliaryAccountingVersion = errors.New("unsupported recorded headless auxiliary accounting version")

// RecordedHeadlessAuxiliaryAccounting is frozen evidence for one outer auxiliary
// provider stream, not an HTTP retry or helper semantic verdict. InvocationID is
// a canonical UUIDv4; RunID is opaque nonempty UTF-8 text. Optional evidence
// distinguishes absence from present unknown values.
// Estimates and reported cost are independent, never additive charges. A value
// copy borrows optional evidence; Clone owns its mutable storage.
type RecordedHeadlessAuxiliaryAccounting struct {
	RunID              string
	InvocationID       string
	Purpose            AuxiliaryAccountingPurpose
	StreamOutcome      inference.StreamOutcome
	Provider, Model    string
	Usage              *llm.Usage
	PricingUsage       *llm.PricingUsage
	RateCard           *llm.RateCard
	ConfiguredEstimate *llm.PriceEstimate
	ReportedCost       *llm.ReportedCost
}

// Clone owns all optional evidence and nested slices, preserving domain cost
// Source. Only the storage projection excludes Source.
func (r RecordedHeadlessAuxiliaryAccounting) Clone() RecordedHeadlessAuxiliaryAccounting {
	evidence := r.evidenceView().Clone()
	r.Usage, r.PricingUsage, r.RateCard = evidence.Usage, evidence.PricingUsage, evidence.RateCard
	r.ConfiguredEstimate, r.ReportedCost = evidence.ConfiguredEstimate, evidence.ReportedCost
	return r
}

// evidenceView supplies only common evidence to the existing private projection
// and clone helpers. No root identity is fabricated, validated or serialized.
func (r RecordedHeadlessAuxiliaryAccounting) evidenceView() RecordedProviderAccounting {
	return RecordedProviderAccounting{
		Provider: r.Provider, Model: r.Model, Usage: r.Usage, PricingUsage: r.PricingUsage,
		RateCard: r.RateCard, ConfiguredEstimate: r.ConfiguredEstimate, ReportedCost: r.ReportedCost,
	}
}

// EncodeRecordedHeadlessAuxiliaryAccounting projects owned allowlisted v1 JSON.
// It never computes estimates, selects rates, or includes execution data.
func EncodeRecordedHeadlessAuxiliaryAccounting(r RecordedHeadlessAuxiliaryAccounting) ([]byte, error) {
	evidence := projectAccounting(r.evidenceView())
	w := headlessAuxiliaryAccountingV1{
		Version: 1, RunID: r.RunID, InvocationID: r.InvocationID,
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
	if err != nil || len(data) > MaxRecordedHeadlessAuxiliaryAccountingBytes || !validAccountingJSON(data) {
		return nil, ErrInvalidRecordedHeadlessAuxiliaryAccounting
	}
	return data, nil
}

// DecodeRecordedHeadlessAuxiliaryAccounting reads bounded strict v1 evidence.
// Missing required scalars, unknown/duplicate keys, malformed Unicode, invalid
// numbers and noncanonical enums reject safely. No evidence is recomputed.
func DecodeRecordedHeadlessAuxiliaryAccounting(data []byte) (RecordedHeadlessAuxiliaryAccounting, error) {
	if len(data) > MaxRecordedHeadlessAuxiliaryAccountingBytes || !validAccountingJSON(data) {
		return RecordedHeadlessAuxiliaryAccounting{}, ErrInvalidRecordedHeadlessAuxiliaryAccounting
	}
	var w headlessAuxiliaryAccountingV1
	if json.Unmarshal(data, &w) != nil {
		return RecordedHeadlessAuxiliaryAccounting{}, ErrInvalidRecordedHeadlessAuxiliaryAccounting
	}
	return w.record()
}

type headlessAuxiliaryAccountingV1 struct {
	Version              int                 `json:"version"`
	RunID                string              `json:"run_id"`
	InvocationID         string              `json:"invocation_id"`
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

func (w *headlessAuxiliaryAccountingV1) UnmarshalJSON(data []byte) error {
	const (
		usageField        = "usage"
		pricingField      = "pricing_usage"
		cardField         = "rate_card"
		estimateField     = "configured_estimate"
		reportedField     = "reported_cost"
		availabilityField = "estimate_availability"
	)
	type plain headlessAuxiliaryAccountingV1
	return decodeAccountingObject(data, (*plain)(w),
		[]string{usageField, pricingField, cardField, estimateField, reportedField},
		"version", "run_id", "invocation_id", "purpose", "stream_outcome", "provider", "model",
		usageField, pricingField, cardField, availabilityField, estimateField, reportedField)
}

func (w headlessAuxiliaryAccountingV1) evidenceView() accountingV1 {
	return accountingV1{
		Provider: w.Provider, Model: w.Model, Usage: w.Usage, PricingUsage: w.PricingUsage,
		RateCard: w.RateCard, EstimateAvailability: w.EstimateAvailability,
		ConfiguredEstimate: w.ConfiguredEstimate, ReportedCost: w.ReportedCost,
	}
}

func (w headlessAuxiliaryAccountingV1) record() (RecordedHeadlessAuxiliaryAccounting, error) {
	if w.Version != 1 {
		return RecordedHeadlessAuxiliaryAccounting{}, ErrRecordedHeadlessAuxiliaryAccountingVersion
	}
	id, idErr := uuid.Parse(w.InvocationID)
	purpose, purposeErr := ParseAuxiliaryAccountingPurpose(w.Purpose)
	outcome, outcomeErr := inference.ParseStreamOutcome(w.StreamOutcome)
	if idErr != nil || id == uuid.Nil || id.Version() != 4 || id.Variant() != uuid.RFC4122 || id.String() != w.InvocationID ||
		w.RunID == "" || !accountingText(w.RunID, w.Provider, w.Model) ||
		purposeErr != nil || !purpose.IsValid() || purpose.String() != w.Purpose ||
		outcomeErr != nil || !outcome.IsValid() || outcome.String() != w.StreamOutcome {
		return RecordedHeadlessAuxiliaryAccounting{}, ErrInvalidRecordedHeadlessAuxiliaryAccounting
	}
	evidence, err := w.evidenceView().evidenceRecord()
	if err != nil {
		return RecordedHeadlessAuxiliaryAccounting{}, ErrInvalidRecordedHeadlessAuxiliaryAccounting
	}
	return RecordedHeadlessAuxiliaryAccounting{
		InvocationID: w.InvocationID, RunID: w.RunID,
		Purpose: purpose, StreamOutcome: outcome, Provider: evidence.Provider, Model: evidence.Model,
		Usage: evidence.Usage, PricingUsage: evidence.PricingUsage, RateCard: evidence.RateCard,
		ConfiguredEstimate: evidence.ConfiguredEstimate, ReportedCost: evidence.ReportedCost,
	}, nil
}

// ReadRecordedHeadlessAuxiliaryAccounting reads owned evidence and checks its
// run, invocation and version against the database envelope and requested key.
// Database identities are preserved; codec errors never expose parser causes.
func ReadRecordedHeadlessAuxiliaryAccounting(ctx context.Context, store *db.Store, runID, invocationID string) (RecordedHeadlessAuxiliaryAccounting, error) {
	row, err := store.GetHeadlessAuxiliaryProviderAccounting(ctx, runID, invocationID)
	if err != nil {
		return RecordedHeadlessAuxiliaryAccounting{}, fmt.Errorf("read recorded headless auxiliary accounting: %w", err)
	}
	r, err := DecodeRecordedHeadlessAuxiliaryAccounting(row.Accounting.PayloadJSON)
	if err != nil {
		return RecordedHeadlessAuxiliaryAccounting{}, err
	}
	if row.Accounting.Version != 1 || r.InvocationID != invocationID || r.InvocationID != row.Accounting.InvocationID ||
		r.RunID != runID {
		return RecordedHeadlessAuxiliaryAccounting{}, ErrInvalidRecordedHeadlessAuxiliaryAccounting
	}
	return r, nil
}
