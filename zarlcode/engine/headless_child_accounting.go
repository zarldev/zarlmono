package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
)

// MaxRecordedHeadlessChildProviderAccountingBytes bounds a complete child record.
const MaxRecordedHeadlessChildProviderAccountingBytes = 1 << 20

// ErrInvalidRecordedHeadlessChildProviderAccounting identifies invalid child evidence or JSON.
var ErrInvalidRecordedHeadlessChildProviderAccounting = errors.New("invalid recorded headless child provider accounting")

// ErrRecordedHeadlessChildProviderAccountingVersion identifies unsupported child storage versions.
var ErrRecordedHeadlessChildProviderAccountingVersion = errors.New("unsupported recorded headless child provider accounting version")

// RecordedHeadlessChildProviderAccounting freezes one descendant provider attempt
// within a headless run. Value copies borrow optional evidence; Clone owns it.
// Absent evidence differs from known zero; estimates and reported cost are independent.
type RecordedHeadlessChildProviderAccounting struct {
	RunID, ChildTaskID string
	Attempt            int
	Provider, Model    string
	Usage              *llm.Usage
	PricingUsage       *llm.PricingUsage
	RateCard           *llm.RateCard
	ConfiguredEstimate *llm.PriceEstimate
	ReportedCost       *llm.ReportedCost
}

func (r RecordedHeadlessChildProviderAccounting) evidenceView() RecordedProviderAccounting {
	return RecordedProviderAccounting{
		Provider: r.Provider, Model: r.Model, Usage: r.Usage, PricingUsage: r.PricingUsage,
		RateCard: r.RateCard, ConfiguredEstimate: r.ConfiguredEstimate, ReportedCost: r.ReportedCost,
	}
}

// Clone returns independently owned evidence, including nested slices and cost Source.
func (r RecordedHeadlessChildProviderAccounting) Clone() RecordedHeadlessChildProviderAccounting {
	e := r.evidenceView().Clone()
	r.Usage, r.PricingUsage, r.RateCard = e.Usage, e.PricingUsage, e.RateCard
	r.ConfiguredEstimate, r.ReportedCost = e.ConfiguredEstimate, e.ReportedCost
	return r
}

// EncodeRecordedHeadlessChildProviderAccounting projects strict flat v1 evidence.
// It does not reprice and omits private execution data and reported cost Source.
func EncodeRecordedHeadlessChildProviderAccounting(r RecordedHeadlessChildProviderAccounting) ([]byte, error) {
	w := headlessChildAccountingWire(r)
	if _, err := w.record(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(w)
	if err != nil || len(data) > MaxRecordedHeadlessChildProviderAccountingBytes || !validAccountingJSON(data) {
		return nil, ErrInvalidRecordedHeadlessChildProviderAccounting
	}
	return data, nil
}

// DecodeRecordedHeadlessChildProviderAccounting validates and owns bounded strict v1 evidence.
func DecodeRecordedHeadlessChildProviderAccounting(data []byte) (RecordedHeadlessChildProviderAccounting, error) {
	if len(data) > MaxRecordedHeadlessChildProviderAccountingBytes || !validAccountingJSON(data) {
		return RecordedHeadlessChildProviderAccounting{}, ErrInvalidRecordedHeadlessChildProviderAccounting
	}
	var w headlessChildAccountingV1
	if json.Unmarshal(data, &w) != nil {
		return RecordedHeadlessChildProviderAccounting{}, ErrInvalidRecordedHeadlessChildProviderAccounting
	}
	return w.record()
}

type headlessChildAccountingV1 struct {
	Version              int                 `json:"version"`
	RunID                string              `json:"run_id"`
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

func headlessChildAccountingWire(r RecordedHeadlessChildProviderAccounting) headlessChildAccountingV1 {
	e := projectAccounting(r.evidenceView())
	return headlessChildAccountingV1{
		Version: 1, RunID: r.RunID, ChildTaskID: r.ChildTaskID, Attempt: r.Attempt,
		Provider: e.Provider, Model: e.Model, Usage: e.Usage, PricingUsage: e.PricingUsage,
		RateCard: e.RateCard, EstimateAvailability: e.EstimateAvailability,
		ConfiguredEstimate: e.ConfiguredEstimate, ReportedCost: e.ReportedCost,
	}
}

func (w *headlessChildAccountingV1) UnmarshalJSON(data []byte) error {
	type plain headlessChildAccountingV1
	const (
		usageField        = "usage"
		pricingField      = "pricing_usage"
		cardField         = "rate_card"
		availabilityField = "estimate_availability"
		estimateField     = "configured_estimate"
		reportedField     = "reported_cost"
	)
	return decodeAccountingObject(data, (*plain)(w),
		[]string{usageField, pricingField, cardField, estimateField, reportedField},
		"version", "run_id", "child_task_id", "attempt", "provider", "model", usageField, pricingField,
		cardField, availabilityField, estimateField, reportedField)
}

func (w headlessChildAccountingV1) record() (RecordedHeadlessChildProviderAccounting, error) {
	if w.Version != 1 {
		return RecordedHeadlessChildProviderAccounting{}, ErrRecordedHeadlessChildProviderAccountingVersion
	}
	id, err := uuid.Parse(w.ChildTaskID)
	if err != nil || id == uuid.Nil || id.Version() != 4 || id.Variant() != uuid.RFC4122 || id.String() != w.ChildTaskID ||
		w.RunID == "" || w.Attempt <= 0 || uint64(w.Attempt) > math.MaxInt64 || !accountingText(w.RunID, w.Provider, w.Model) {
		return RecordedHeadlessChildProviderAccounting{}, ErrInvalidRecordedHeadlessChildProviderAccounting
	}
	e, err := (accountingV1{
		Provider: w.Provider, Model: w.Model, Usage: w.Usage, PricingUsage: w.PricingUsage,
		RateCard: w.RateCard, EstimateAvailability: w.EstimateAvailability,
		ConfiguredEstimate: w.ConfiguredEstimate, ReportedCost: w.ReportedCost,
	}).evidenceRecord()
	if err != nil {
		return RecordedHeadlessChildProviderAccounting{}, ErrInvalidRecordedHeadlessChildProviderAccounting
	}
	return RecordedHeadlessChildProviderAccounting{
		RunID: w.RunID, ChildTaskID: w.ChildTaskID, Attempt: w.Attempt,
		Provider: e.Provider, Model: e.Model, Usage: e.Usage, PricingUsage: e.PricingUsage,
		RateCard: e.RateCard, ConfiguredEstimate: e.ConfiguredEstimate, ReportedCost: e.ReportedCost,
	}, nil
}

// ReadRecordedHeadlessChildProviderAccounting verifies the stored envelope and
// returns independent frozen evidence without repricing. Database errors retain their identities.
func ReadRecordedHeadlessChildProviderAccounting(ctx context.Context, store *db.Store, runID, childTaskID string, attempt int) (RecordedHeadlessChildProviderAccounting, error) {
	row, err := store.GetHeadlessChildProviderAccounting(ctx, runID, childTaskID, attempt)
	if err != nil {
		return RecordedHeadlessChildProviderAccounting{}, fmt.Errorf("read recorded headless child provider accounting: %w", err)
	}
	r, err := DecodeRecordedHeadlessChildProviderAccounting(row.Accounting.PayloadJSON)
	if err != nil {
		return RecordedHeadlessChildProviderAccounting{}, err
	}
	if row.Accounting.Version != 1 || r.RunID != runID || r.ChildTaskID != childTaskID || r.Attempt != attempt ||
		r.ChildTaskID != row.Accounting.ChildTaskID || r.Attempt != row.Accounting.Attempt {
		return RecordedHeadlessChildProviderAccounting{}, ErrInvalidRecordedHeadlessChildProviderAccounting
	}
	return r, nil
}
