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

// MaxRecordedSessionManualAccountingBytes bounds a complete manual storage record.
const MaxRecordedSessionManualAccountingBytes = 1 << 20

// ErrInvalidRecordedSessionManualAccounting identifies invalid manual evidence without exposing private input.
var ErrInvalidRecordedSessionManualAccounting = errors.New("invalid recorded session manual accounting")

// ErrRecordedSessionManualAccountingVersion identifies unsupported manual storage versions.
var ErrRecordedSessionManualAccountingVersion = errors.New("unsupported recorded session manual accounting version")

// RecordedSessionManualAccounting is frozen evidence for one manual compaction
// provider stream. SessionID is opaque nonempty UTF-8; InvocationID is a canonical
// lowercase UUIDv4. Optional snapshots distinguish absence from known zero.
// Estimates and reported cost are independent, not additive. Value copies borrow
// optional evidence; Clone returns independently owned evidence.
type RecordedSessionManualAccounting struct {
	SessionID, InvocationID string
	Purpose                 AuxiliaryAccountingPurpose
	StreamOutcome           inference.StreamOutcome
	Provider, Model         string
	Usage                   *llm.Usage
	PricingUsage            *llm.PricingUsage
	RateCard                *llm.RateCard
	ConfiguredEstimate      *llm.PriceEstimate
	ReportedCost            *llm.ReportedCost
}

// Clone owns all optional evidence and nested slices, preserving domain cost Source.
// Only the storage projection excludes Source.
func (r RecordedSessionManualAccounting) Clone() RecordedSessionManualAccounting {
	evidence := r.evidenceView().Clone()
	r.Usage, r.PricingUsage, r.RateCard = evidence.Usage, evidence.PricingUsage, evidence.RateCard
	r.ConfiguredEstimate, r.ReportedCost = evidence.ConfiguredEstimate, evidence.ReportedCost
	return r
}

func (r RecordedSessionManualAccounting) evidenceView() RecordedProviderAccounting {
	return RecordedProviderAccounting{
		Provider: r.Provider, Model: r.Model, Usage: r.Usage, PricingUsage: r.PricingUsage,
		RateCard: r.RateCard, ConfiguredEstimate: r.ConfiguredEstimate, ReportedCost: r.ReportedCost,
	}
}

// EncodeRecordedSessionManualAccounting projects allowlisted strict v1 JSON.
// It does not compute estimates or include private execution data.
func EncodeRecordedSessionManualAccounting(r RecordedSessionManualAccounting) ([]byte, error) {
	evidence := projectAccounting(r.evidenceView())
	w := sessionManualAccountingV1{
		Version: 1, SessionID: r.SessionID, InvocationID: r.InvocationID,
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
	if err != nil || len(data) > MaxRecordedSessionManualAccountingBytes || !validAccountingJSON(data) {
		return nil, ErrInvalidRecordedSessionManualAccounting
	}
	return data, nil
}

// DecodeRecordedSessionManualAccounting reads bounded strict v1 evidence.
// Unknown or duplicate keys, malformed Unicode, missing required fields and
// noncanonical enum values reject without returning partial evidence.
func DecodeRecordedSessionManualAccounting(data []byte) (RecordedSessionManualAccounting, error) {
	if len(data) > MaxRecordedSessionManualAccountingBytes || !validAccountingJSON(data) {
		return RecordedSessionManualAccounting{}, ErrInvalidRecordedSessionManualAccounting
	}
	var w sessionManualAccountingV1
	if json.Unmarshal(data, &w) != nil {
		return RecordedSessionManualAccounting{}, ErrInvalidRecordedSessionManualAccounting
	}
	return w.record()
}

type sessionManualAccountingV1 struct {
	Version              int                 `json:"version"`
	SessionID            string              `json:"session_id"`
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

func (w *sessionManualAccountingV1) UnmarshalJSON(data []byte) error {
	const (
		usageField    = "usage"
		pricingField  = "pricing_usage"
		cardField     = "rate_card"
		estimateField = "configured_estimate"
		reportedField = "reported_cost"
	)
	type plain sessionManualAccountingV1
	return decodeAccountingObject(data, (*plain)(w),
		[]string{usageField, pricingField, cardField, estimateField, reportedField},
		"version", "session_id", "invocation_id", "purpose", "stream_outcome", "provider", "model",
		usageField, pricingField, cardField, "estimate_availability", estimateField, reportedField)
}
func (w sessionManualAccountingV1) evidenceView() accountingV1 {
	return accountingV1{
		Provider: w.Provider, Model: w.Model, Usage: w.Usage, PricingUsage: w.PricingUsage,
		RateCard: w.RateCard, EstimateAvailability: w.EstimateAvailability,
		ConfiguredEstimate: w.ConfiguredEstimate, ReportedCost: w.ReportedCost,
	}
}

func (w sessionManualAccountingV1) record() (RecordedSessionManualAccounting, error) {
	if w.Version != 1 {
		return RecordedSessionManualAccounting{}, ErrRecordedSessionManualAccountingVersion
	}
	id, idErr := uuid.Parse(w.InvocationID)
	purpose, purposeErr := ParseAuxiliaryAccountingPurpose(w.Purpose)
	outcome, outcomeErr := inference.ParseStreamOutcome(w.StreamOutcome)
	if idErr != nil || id == uuid.Nil || id.Version() != 4 || id.Variant() != uuid.RFC4122 || id.String() != w.InvocationID ||
		w.SessionID == "" || !accountingText(w.SessionID, w.Provider, w.Model) ||
		purposeErr != nil || purpose.String() != w.Purpose ||
		purpose != AuxiliaryAccountingPurposes.PURPOSESUMMARY && purpose != AuxiliaryAccountingPurposes.PURPOSEEXECUTIVE && purpose != AuxiliaryAccountingPurposes.PURPOSEHANDOVER ||
		outcomeErr != nil || !outcome.IsValid() || outcome.String() != w.StreamOutcome {
		return RecordedSessionManualAccounting{}, ErrInvalidRecordedSessionManualAccounting
	}
	evidence, err := w.evidenceView().evidenceRecord()
	if err != nil {
		return RecordedSessionManualAccounting{}, ErrInvalidRecordedSessionManualAccounting
	}
	return RecordedSessionManualAccounting{
		SessionID: w.SessionID, InvocationID: w.InvocationID, Purpose: purpose, StreamOutcome: outcome,
		Provider: evidence.Provider, Model: evidence.Model, Usage: evidence.Usage, PricingUsage: evidence.PricingUsage,
		RateCard: evidence.RateCard, ConfiguredEstimate: evidence.ConfiguredEstimate, ReportedCost: evidence.ReportedCost,
	}, nil
}

// ReadRecordedSessionManualAccounting reads owned evidence and verifies the
// session, invocation and version against the DB envelope and requested key.
// Database error identities are preserved; codec failures omit parser details.
func ReadRecordedSessionManualAccounting(ctx context.Context, store *db.Store, sessionID, invocationID string) (RecordedSessionManualAccounting, error) {
	row, err := store.GetSessionManualProviderAccounting(ctx, sessionID, invocationID)
	if err != nil {
		return RecordedSessionManualAccounting{}, fmt.Errorf("read recorded session manual accounting: %w", err)
	}
	r, err := DecodeRecordedSessionManualAccounting(row.Accounting.PayloadJSON)
	if err != nil {
		return RecordedSessionManualAccounting{}, err
	}
	if row.Accounting.Version != 1 || r.SessionID != sessionID || r.InvocationID != invocationID || r.InvocationID != row.Accounting.InvocationID {
		return RecordedSessionManualAccounting{}, ErrInvalidRecordedSessionManualAccounting
	}
	return r, nil
}
