package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/zarldev/zarlmono/zarlcode/engine/internal/accountingwire"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
)

// MaxRecordedHeadlessProviderAccountingBytes bounds a complete headless storage record.
const MaxRecordedHeadlessProviderAccountingBytes = 1 << 20

// ErrInvalidRecordedHeadlessProviderAccounting identifies invalid headless evidence
// or JSON without exposing input text or underlying parser diagnostics.
var ErrInvalidRecordedHeadlessProviderAccounting = errors.New("invalid recorded headless provider accounting")

// ErrRecordedHeadlessProviderAccountingVersion identifies unsupported headless storage versions.
var ErrRecordedHeadlessProviderAccountingVersion = errors.New("unsupported recorded headless provider accounting version")

// RecordedHeadlessProviderAccounting is frozen evidence for a provider occurrence
// in a pursue execution, owned by a headless run rather than a session receipt.
// Optional snapshots distinguish absence from present unknown evidence. Estimates
// and reported cost are independent, never additive charges. Value copies borrow
// optional snapshots; Clone owns their mutable storage.
type RecordedHeadlessProviderAccounting struct {
	RunID              string
	PursueAttempt      int
	ProviderOrdinal    int
	Provider, Model    string
	Usage              *llm.Usage
	PricingUsage       *llm.PricingUsage
	RateCard           *llm.RateCard
	ConfiguredEstimate *llm.PriceEstimate
	ReportedCost       *llm.ReportedCost
}

// evidenceView supplies only common evidence to PA1's projection/clone helpers.
// No root identity is manufactured and this adapter is never serialized.
func (r RecordedHeadlessProviderAccounting) evidenceView() RecordedProviderAccounting {
	return RecordedProviderAccounting{
		Provider: r.Provider, Model: r.Model, Usage: r.Usage, PricingUsage: r.PricingUsage,
		RateCard: r.RateCard, ConfiguredEstimate: r.ConfiguredEstimate, ReportedCost: r.ReportedCost,
	}
}

// Clone returns independently owned optional evidence, including nested slices.
// Domain cost Source is preserved; only the storage allowlist excludes Source.
func (r RecordedHeadlessProviderAccounting) Clone() RecordedHeadlessProviderAccounting {
	evidence := r.evidenceView().Clone()
	r.Usage, r.PricingUsage, r.RateCard = evidence.Usage, evidence.PricingUsage, evidence.RateCard
	r.ConfiguredEstimate, r.ReportedCost = evidence.ConfiguredEstimate, evidence.ReportedCost
	return r
}

// EncodeRecordedHeadlessProviderAccounting projects strict v1 JSON containing
// headless identity and allowlisted frozen evidence. It never computes estimates
// or selects rates, and excludes execution data and reported cost Source.
func EncodeRecordedHeadlessProviderAccounting(r RecordedHeadlessProviderAccounting) ([]byte, error) {
	w := headlessAccountingWire(r)
	if _, err := w.record(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(w)
	if err != nil || len(data) > MaxRecordedHeadlessProviderAccountingBytes || !validAccountingJSON(data) {
		return nil, ErrInvalidRecordedHeadlessProviderAccounting
	}
	return data, nil
}

// DecodeRecordedHeadlessProviderAccounting reads owned, bounded strict v1
// evidence. Unknown, duplicate, wrong-case, invalid and missing required fields
// reject safely. No evidence is recomputed, repaired or repriced.
func DecodeRecordedHeadlessProviderAccounting(data []byte) (RecordedHeadlessProviderAccounting, error) {
	if len(data) > MaxRecordedHeadlessProviderAccountingBytes || !validAccountingJSON(data) {
		return RecordedHeadlessProviderAccounting{}, ErrInvalidRecordedHeadlessProviderAccounting
	}
	var w headlessAccountingV1
	if json.Unmarshal(data, &w) != nil {
		return RecordedHeadlessProviderAccounting{}, ErrInvalidRecordedHeadlessProviderAccounting
	}
	return w.record()
}

type headlessAccountingV1 struct {
	Version              int                 `json:"version"`
	RunID                string              `json:"run_id"`
	PursueAttempt        int                 `json:"pursue_attempt"`
	ProviderOrdinal      int                 `json:"provider_ordinal"`
	Provider             string              `json:"provider"`
	Model                string              `json:"model"`
	Usage                *accountingUsage    `json:"usage"`
	PricingUsage         *accountingPricing  `json:"pricing_usage"`
	RateCard             *accountingCard     `json:"rate_card"`
	EstimateAvailability string              `json:"estimate_availability"`
	ConfiguredEstimate   *accountingEstimate `json:"configured_estimate"`
	ReportedCost         *accountingReported `json:"reported_cost"`
}

func headlessAccountingWire(r RecordedHeadlessProviderAccounting) headlessAccountingV1 {
	w := projectAccounting(r.evidenceView())
	return headlessAccountingV1{
		Version: w.Version, RunID: r.RunID, PursueAttempt: r.PursueAttempt, ProviderOrdinal: r.ProviderOrdinal,
		Provider: w.Provider, Model: w.Model, Usage: w.Usage, PricingUsage: w.PricingUsage,
		RateCard: w.RateCard, EstimateAvailability: w.EstimateAvailability,
		ConfiguredEstimate: w.ConfiguredEstimate, ReportedCost: w.ReportedCost,
	}
}

func (w *headlessAccountingV1) UnmarshalJSON(data []byte) error {
	// Keep the headless scanner's vocabulary local rather than changing root/child
	// codecs just to share their wire-key spellings.
	const (
		versionField      = "version"
		providerField     = "provider"
		modelField        = "model"
		usageField        = "usage"
		pricingField      = "pricing_usage"
		cardField         = "rate_card"
		availabilityField = "estimate_availability"
		estimateField     = "configured_estimate"
		reportedField     = "reported_cost"
	)
	type plain headlessAccountingV1
	return decodeAccountingObject(data, (*plain)(w),
		[]string{usageField, pricingField, cardField, estimateField, reportedField},
		versionField, "run_id", "pursue_attempt", "provider_ordinal", providerField, modelField,
		usageField, pricingField, cardField, availabilityField, estimateField, reportedField)
}

// Headless identity is checked here, independently of the root receipt schema.
// Only the common private evidence converters are shared with root/child v1.
func (w headlessAccountingV1) record() (RecordedHeadlessProviderAccounting, error) {
	if w.Version != 1 {
		return RecordedHeadlessProviderAccounting{}, ErrRecordedHeadlessProviderAccountingVersion
	}
	if w.RunID == "" || w.PursueAttempt <= 0 || uint64(w.PursueAttempt) > math.MaxInt64 ||
		w.ProviderOrdinal <= 0 || uint64(w.ProviderOrdinal) > math.MaxInt64 || !accountingText(w.RunID, w.Provider, w.Model) {
		return RecordedHeadlessProviderAccounting{}, ErrInvalidRecordedHeadlessProviderAccounting
	}
	availability, err := accountingwire.ParseEstimateAvailability(w.EstimateAvailability)
	if err != nil || availability.String() != w.EstimateAvailability ||
		availability != accountingAvailability(w.RateCard != nil, w.PricingUsage != nil, w.ConfiguredEstimate != nil) {
		return RecordedHeadlessProviderAccounting{}, ErrInvalidRecordedHeadlessProviderAccounting
	}
	r := RecordedHeadlessProviderAccounting{
		RunID: w.RunID, PursueAttempt: w.PursueAttempt, ProviderOrdinal: w.ProviderOrdinal, Provider: w.Provider, Model: w.Model,
	}
	if w.Usage != nil {
		u, err := w.Usage.usage()
		if err != nil {
			return RecordedHeadlessProviderAccounting{}, ErrInvalidRecordedHeadlessProviderAccounting
		}
		r.Usage = &u
	}
	if w.PricingUsage != nil {
		u, err := w.PricingUsage.usage()
		if err != nil {
			return RecordedHeadlessProviderAccounting{}, ErrInvalidRecordedHeadlessProviderAccounting
		}
		r.PricingUsage = &u
	}
	if w.RateCard != nil {
		c, err := w.RateCard.card()
		if err != nil {
			return RecordedHeadlessProviderAccounting{}, ErrInvalidRecordedHeadlessProviderAccounting
		}
		r.RateCard = &c
	}
	if w.ConfiguredEstimate != nil {
		e, err := w.ConfiguredEstimate.estimate()
		if err != nil {
			return RecordedHeadlessProviderAccounting{}, ErrInvalidRecordedHeadlessProviderAccounting
		}
		r.ConfiguredEstimate = &e
	}
	if c := w.ReportedCost; c != nil {
		if !accountingAmount(c.Amount) || !accountingText(c.Currency) {
			return RecordedHeadlessProviderAccounting{}, ErrInvalidRecordedHeadlessProviderAccounting
		}
		r.ReportedCost = &llm.ReportedCost{Currency: llm.Currency(c.Currency), Reported: c.Reported, CoverageKnown: c.CoverageKnown}
		if c.Reported {
			r.ReportedCost.Amount = c.Amount
		}
	}
	return r, nil
}

// ReadRecordedHeadlessProviderAccounting verifies decoded identity/version against
// its database envelope and run scope. Database absence/corruption identities are
// preserved; payload failures use headless codec identities. Reads never reprice.
func ReadRecordedHeadlessProviderAccounting(ctx context.Context, store *db.Store, runID string, pursueAttempt, providerOrdinal int) (RecordedHeadlessProviderAccounting, error) {
	row, err := store.GetHeadlessProviderAccounting(ctx, runID, pursueAttempt, providerOrdinal)
	if err != nil {
		return RecordedHeadlessProviderAccounting{}, fmt.Errorf("read recorded headless provider accounting: %w", err)
	}
	r, err := DecodeRecordedHeadlessProviderAccounting(row.Accounting.PayloadJSON)
	if err != nil {
		return RecordedHeadlessProviderAccounting{}, err
	}
	if row.Accounting.Version != 1 || r.RunID != runID || r.PursueAttempt != row.Accounting.PursueAttempt ||
		r.ProviderOrdinal != row.Accounting.ProviderOrdinal || r.PursueAttempt != pursueAttempt || r.ProviderOrdinal != providerOrdinal {
		return RecordedHeadlessProviderAccounting{}, ErrInvalidRecordedHeadlessProviderAccounting
	}
	return r, nil
}
