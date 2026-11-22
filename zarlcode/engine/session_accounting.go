package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zarlcode/engine/internal/accountingwire"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// MaxRecordedProviderAccountingBytes bounds a complete versioned storage record.
const MaxRecordedProviderAccountingBytes = 1024 * 1024

// ErrInvalidRecordedProviderAccounting identifies invalid accounting snapshots or
// storage JSON. Diagnostics never expose input text or underlying parser errors.
var ErrInvalidRecordedProviderAccounting = errors.New("invalid recorded provider accounting")

// ErrRecordedProviderAccountingVersion identifies unsupported storage versions.
var ErrRecordedProviderAccountingVersion = errors.New("unsupported recorded provider accounting version")

// RecordedProviderAccounting is settled accounting evidence for one recorded root
// request receipt. Estimates and reported cost are independent frozen snapshots,
// never additive charges. Pointer absence differs from present unknown evidence.
// Value copies borrow optional snapshots; Clone owns their mutable storage.
type RecordedProviderAccounting struct {
	RequestBatchID, TaskID string
	Attempt                int
	Provider, Model        string
	Usage                  *llm.Usage
	PricingUsage           *llm.PricingUsage
	RateCard               *llm.RateCard
	ConfiguredEstimate     *llm.PriceEstimate
	ReportedCost           *llm.ReportedCost
}

// Clone returns independently owned optional snapshots, including nested slices.
// It preserves domain evidence; only the storage projection excludes cost Source.
func (r RecordedProviderAccounting) Clone() RecordedProviderAccounting {
	if r.Usage != nil {
		usage := *r.Usage
		r.Usage = &usage
	}
	if r.PricingUsage != nil {
		usage := r.PricingUsage.Clone()
		r.PricingUsage = &usage
	}
	if r.RateCard != nil {
		card := r.RateCard.Clone()
		r.RateCard = &card
	}
	if r.ConfiguredEstimate != nil {
		estimate := r.ConfiguredEstimate.Clone()
		r.ConfiguredEstimate = &estimate
	}
	if r.ReportedCost != nil {
		cost := *r.ReportedCost
		r.ReportedCost = &cost
	}
	return r
}

// EncodeRecordedProviderAccounting projects allowlisted evidence into owned v1
// JSON. It validates storage scalars and never selects rates or computes prices.
// Undefined numbers behind unknown flags are projected to unknown, not known zero.
func EncodeRecordedProviderAccounting(r RecordedProviderAccounting) ([]byte, error) {
	wire := projectAccounting(r)
	if _, err := wire.record(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(wire)
	if err != nil || len(data) > MaxRecordedProviderAccountingBytes || !validAccountingJSON(data) {
		return nil, ErrInvalidRecordedProviderAccounting
	}
	return data, nil
}

// DecodeRecordedProviderAccounting reads strict, bounded v1 JSON into owned
// evidence. Unknown/duplicate fields, invalid scalars and trailing JSON are
// rejected with safe semantic identities. No estimates are recomputed on read.
func DecodeRecordedProviderAccounting(data []byte) (RecordedProviderAccounting, error) {
	if len(data) > MaxRecordedProviderAccountingBytes || !validAccountingJSON(data) {
		return RecordedProviderAccounting{}, ErrInvalidRecordedProviderAccounting
	}
	var wire accountingV1
	if json.Unmarshal(data, &wire) != nil {
		return RecordedProviderAccounting{}, ErrInvalidRecordedProviderAccounting
	}
	return wire.record()
}

func validAccountingJSON(data []byte) bool {
	// Validate syntax before the existing surrogate checker, which expects valid
	// escapes. Unlike encoding/json's replacement behavior, IDs are not repaired.
	return utf8.Valid(data) && json.Valid(data) && rateCardUnicodeEscapes(data)
}

// Explicit DTOs are deliberately independent of llm's JSON representation.
// Nullable quantities/rates encode unknown versus a known numeric zero. Durations
// are integer nanoseconds, preserving zero/unknown and exact lifetime subsets.
type accountingV1 struct {
	Version              int                 `json:"version"`
	RequestBatchID       string              `json:"request_batch_id"`
	TaskID               string              `json:"task_id"`
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

type accountingUsage struct {
	PromptTokens          int  `json:"prompt_tokens"`
	CompletionTokens      int  `json:"completion_tokens"`
	TotalTokens           int  `json:"total_tokens"`
	CachedTokens          int  `json:"cached_tokens"`
	CacheCreationTokens   int  `json:"cache_creation_tokens"`
	CacheCreation5mTokens int  `json:"cache_creation_5m_tokens"`
	CacheCreation1hTokens int  `json:"cache_creation_1h_tokens"`
	ReasoningTokens       *int `json:"reasoning_tokens"`
}

type accountingPricing struct {
	Input                     accountingInput  `json:"input"`
	Output                    accountingOutput `json:"output"`
	AdditionalChargesPossible bool             `json:"additional_charges_possible"`
}

type accountingInput struct {
	Total            *int64                    `json:"total"`
	CacheRead        *int64                    `json:"cache_read"`
	CacheWrite       *int64                    `json:"cache_write"`
	Other            *int64                    `json:"other"`
	WritesByLifetime []accountingWriteQuantity `json:"writes_by_lifetime"`
	PartitionKnown   bool                      `json:"partition_known"`
}

type accountingOutput struct {
	Total *int64 `json:"total"`
	Other *int64 `json:"other"`
}

type accountingWriteQuantity struct {
	Lifetime int64  `json:"lifetime_ns"`
	Quantity *int64 `json:"quantity"`
}

type accountingReference struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
	Entry    string `json:"entry"`
}

type accountingProvenance struct {
	Source     string    `json:"source"`
	Snapshot   string    `json:"snapshot"`
	Revision   string    `json:"revision"`
	ObservedAt time.Time `json:"observed_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type accountingCard struct {
	Reference       accountingReference   `json:"reference"`
	PricingProvider string                `json:"pricing_provider"`
	Currency        string                `json:"currency"`
	Billing         string                `json:"billing"`
	Provenance      accountingProvenance  `json:"provenance"`
	Applicable      bool                  `json:"applicable"`
	TokenOnly       bool                  `json:"token_only"`
	FreshInput      *float64              `json:"fresh_input_per_million"`
	CacheRead       *float64              `json:"cache_read_per_million"`
	Output          *float64              `json:"output_per_million"`
	CacheWrites     []accountingWriteRate `json:"cache_writes"`
}

type accountingWriteRate struct {
	Lifetime int64    `json:"lifetime_ns"`
	Rate     *float64 `json:"per_million"`
}

type accountingEstimate struct {
	Reference        accountingReference  `json:"reference"`
	PricingProvider  string               `json:"pricing_provider"`
	Currency         string               `json:"currency"`
	Billing          string               `json:"billing"`
	Provenance       accountingProvenance `json:"provenance"`
	InputAmount      float64              `json:"input_amount"`
	OutputAmount     float64              `json:"output_amount"`
	Subtotal         float64              `json:"subtotal"`
	HasKnownSubtotal bool                 `json:"has_known_subtotal"`
	Complete         bool                 `json:"complete"`
	Issues           []accountingIssue    `json:"issues"`
}

type accountingIssue struct {
	Kind     string `json:"kind"`
	Field    string `json:"field"`
	Lifetime int64  `json:"lifetime_ns"`
}

type accountingReported struct {
	Amount        float64 `json:"amount"`
	Currency      string  `json:"currency"`
	Reported      bool    `json:"reported"`
	CoverageKnown bool    `json:"coverage_known"`
}

// Reuse the existing strict object scanner without inheriting rate-card parser
// errors or changing its contract. Only explicitly optional members permit null
// or omission; every other member must be present and non-null. Nested DTOs use
// the same scanner, so duplicate and unknown members are rejected at every level.
func decodeAccountingObject(data []byte, value any, optional []string, fields ...string) error {
	object, err := rateCardObject(data, fields...)
	if err != nil {
		return ErrInvalidRecordedProviderAccounting
	}
	for _, raw := range object {
		if len(raw) > 0 && (raw[0] == '-' || (raw[0] >= '0' && raw[0] <= '9')) && !validAccountingNumber(raw) {
			return ErrInvalidRecordedProviderAccounting
		}
	}
	for _, field := range fields {
		if !slices.Contains(optional, field) && (len(object[field]) == 0 || bytes.Equal(object[field], []byte("null"))) {
			return ErrInvalidRecordedProviderAccounting
		}
	}
	if json.Unmarshal(data, value) != nil {
		return ErrInvalidRecordedProviderAccounting
	}
	return nil
}

// Numeric underflow must not turn nonzero stored evidence into known zero.
func validAccountingNumber(raw []byte) bool {
	n, err := strconv.ParseFloat(string(raw), 64)
	if err != nil {
		return false
	}
	mantissa, _, _ := strings.Cut(strings.ToLower(string(raw)), "e")
	return n != 0 || !strings.ContainsAny(mantissa, "123456789")
}

func accountingText(values ...string) bool {
	for _, value := range values {
		if !utf8.ValidString(value) {
			return false
		}
	}
	return true
}

func (w *accountingV1) UnmarshalJSON(data []byte) error {
	type plain accountingV1
	return decodeAccountingObject(data, (*plain)(w),
		[]string{"usage", "pricing_usage", "rate_card", "configured_estimate", "reported_cost"},
		"version", "request_batch_id", "task_id", "attempt", "provider", "model", "usage", "pricing_usage",
		"rate_card", "estimate_availability", "configured_estimate", "reported_cost")
}

func (w *accountingUsage) UnmarshalJSON(data []byte) error {
	type plain accountingUsage
	return decodeAccountingObject(data, (*plain)(w), []string{"reasoning_tokens"},
		"prompt_tokens", "completion_tokens", "total_tokens", "cached_tokens", "cache_creation_tokens",
		"cache_creation_5m_tokens", "cache_creation_1h_tokens", "reasoning_tokens")
}

func (w *accountingPricing) UnmarshalJSON(data []byte) error {
	type plain accountingPricing
	return decodeAccountingObject(data, (*plain)(w), nil, "input", "output", "additional_charges_possible")
}

func (w *accountingInput) UnmarshalJSON(data []byte) error {
	type plain accountingInput
	return decodeAccountingObject(data, (*plain)(w),
		[]string{"total", "cache_read", "cache_write", "other", "writes_by_lifetime"},
		"total", "cache_read", "cache_write", "other", "writes_by_lifetime", "partition_known")
}

func (w *accountingOutput) UnmarshalJSON(data []byte) error {
	type plain accountingOutput
	return decodeAccountingObject(data, (*plain)(w), []string{"total", "other"}, "total", "other")
}

func (w *accountingWriteQuantity) UnmarshalJSON(data []byte) error {
	type plain accountingWriteQuantity
	return decodeAccountingObject(data, (*plain)(w), []string{"quantity"}, "lifetime_ns", "quantity")
}

func (w *accountingReference) UnmarshalJSON(data []byte) error {
	type plain accountingReference
	return decodeAccountingObject(data, (*plain)(w), nil, "id", "revision", "entry")
}

func (w *accountingProvenance) UnmarshalJSON(data []byte) error {
	type plain accountingProvenance
	return decodeAccountingObject(data, (*plain)(w), nil, "source", "snapshot", "revision", "observed_at", "expires_at")
}

func (w *accountingCard) UnmarshalJSON(data []byte) error {
	type plain accountingCard
	return decodeAccountingObject(data, (*plain)(w),
		[]string{"fresh_input_per_million", "cache_read_per_million", "output_per_million", "cache_writes"},
		"reference", "pricing_provider", "currency", "billing", "provenance", "applicable", "token_only",
		"fresh_input_per_million", "cache_read_per_million", "output_per_million", "cache_writes")
}

func (w *accountingWriteRate) UnmarshalJSON(data []byte) error {
	type plain accountingWriteRate
	return decodeAccountingObject(data, (*plain)(w), []string{"per_million"}, "lifetime_ns", "per_million")
}

func (w *accountingEstimate) UnmarshalJSON(data []byte) error {
	type plain accountingEstimate
	return decodeAccountingObject(data, (*plain)(w), []string{"issues"},
		"reference", "pricing_provider", "currency", "billing", "provenance", "input_amount", "output_amount",
		"subtotal", "has_known_subtotal", "complete", "issues")
}

func (w *accountingIssue) UnmarshalJSON(data []byte) error {
	type plain accountingIssue
	return decodeAccountingObject(data, (*plain)(w), nil, "kind", "field", "lifetime_ns")
}

func (w *accountingReported) UnmarshalJSON(data []byte) error {
	type plain accountingReported
	return decodeAccountingObject(data, (*plain)(w), nil, "amount", "currency", "reported", "coverage_known")
}

func accountingAvailability(card, usage, estimate bool) accountingwire.EstimateAvailability {
	switch {
	case estimate:
		return accountingwire.EstimateAvailabilities.ESTIMATEAVAILABLE
	case !card && !usage:
		return accountingwire.EstimateAvailabilities.ESTIMATEBOTHMISSING
	case !card:
		return accountingwire.EstimateAvailabilities.ESTIMATEMISSINGCARD
	case !usage:
		return accountingwire.EstimateAvailabilities.ESTIMATEMISSINGUSAGE
	default:
		return accountingwire.EstimateAvailabilities.ESTIMATENOTCOMPUTED
	}
}

func projectAccounting(r RecordedProviderAccounting) accountingV1 {
	w := accountingV1{
		Version: 1, RequestBatchID: r.RequestBatchID, TaskID: r.TaskID, Attempt: r.Attempt,
		Provider: r.Provider, Model: r.Model,
		EstimateAvailability: accountingAvailability(r.RateCard != nil, r.PricingUsage != nil, r.ConfiguredEstimate != nil).String(),
	}
	if u := r.Usage; u != nil {
		w.Usage = &accountingUsage{
			PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens, TotalTokens: u.TotalTokens,
			CachedTokens: u.CachedTokens, CacheCreationTokens: u.CacheCreationTokens,
			CacheCreation5mTokens: u.CacheCreation5mTokens, CacheCreation1hTokens: u.CacheCreation1hTokens,
		}
		if u.ReasoningTokensReported {
			n := u.ReasoningTokens
			w.Usage.ReasoningTokens = &n
		}
	}
	if u := r.PricingUsage; u != nil {
		w.PricingUsage = &accountingPricing{
			Input: accountingInput{
				Total: projectQuantity(u.Input.Total), CacheRead: projectQuantity(u.Input.CacheRead),
				CacheWrite: projectQuantity(u.Input.CacheWrite), Other: projectQuantity(u.Input.Other),
				PartitionKnown: u.Input.PartitionKnown,
			},
			Output:                    accountingOutput{Total: projectQuantity(u.Output.Total), Other: projectQuantity(u.Output.Other)},
			AdditionalChargesPossible: u.AdditionalChargesPossible,
		}
		if u.Input.WritesByLifetime != nil {
			w.PricingUsage.Input.WritesByLifetime = make([]accountingWriteQuantity, len(u.Input.WritesByLifetime))
			for i, row := range u.Input.WritesByLifetime {
				w.PricingUsage.Input.WritesByLifetime[i] = accountingWriteQuantity{Lifetime: int64(row.Lifetime), Quantity: projectQuantity(row.Quantity)}
			}
		}
	}
	if c := r.RateCard; c != nil {
		w.RateCard = &accountingCard{
			Reference: projectReference(c.Reference), PricingProvider: string(c.PricingProvider), Currency: string(c.Currency),
			Billing: c.Billing.String(), Provenance: projectProvenance(c.Provenance), Applicable: c.Applicable, TokenOnly: c.TokenOnly,
			FreshInput: projectRate(c.FreshInput), CacheRead: projectRate(c.CacheRead), Output: projectRate(c.Output),
		}
		if c.CacheWrites != nil {
			w.RateCard.CacheWrites = make([]accountingWriteRate, len(c.CacheWrites))
			for i, row := range c.CacheWrites {
				w.RateCard.CacheWrites[i] = accountingWriteRate{Lifetime: int64(row.Lifetime), Rate: projectRate(row.Rate)}
			}
		}
	}
	if e := r.ConfiguredEstimate; e != nil {
		w.ConfiguredEstimate = &accountingEstimate{
			Reference: projectReference(e.Reference), PricingProvider: string(e.PricingProvider), Currency: string(e.Currency),
			Billing: e.Billing.String(), Provenance: projectProvenance(e.Provenance),
			HasKnownSubtotal: e.HasKnownSubtotal, Complete: e.Complete,
		}
		// Direction amounts remain evidence even when only their sum overflowed.
		w.ConfiguredEstimate.InputAmount, w.ConfiguredEstimate.OutputAmount = e.InputAmount, e.OutputAmount
		if e.HasKnownSubtotal {
			w.ConfiguredEstimate.Subtotal = e.Subtotal
		}
		if e.Issues != nil {
			w.ConfiguredEstimate.Issues = make([]accountingIssue, len(e.Issues))
			for i, issue := range e.Issues {
				w.ConfiguredEstimate.Issues[i] = accountingIssue{Kind: issue.Kind.String(), Field: issue.Field, Lifetime: int64(issue.Lifetime)}
			}
		}
	}
	if c := r.ReportedCost; c != nil {
		w.ReportedCost = &accountingReported{Currency: string(c.Currency), Reported: c.Reported, CoverageKnown: c.CoverageKnown}
		if c.Reported {
			w.ReportedCost.Amount = c.Amount
		}
	}
	return w
}

func projectQuantity(q llm.TokenQuantity) *int64 {
	if !q.Known {
		return nil
	}
	return &q.Tokens
}

func projectRate(r llm.TokenRate) *float64 {
	if !r.Known {
		return nil
	}
	return &r.PerMillionTokens
}

func projectReference(r llm.RateCardReference) accountingReference {
	return accountingReference{ID: r.ID, Revision: r.Revision, Entry: r.Entry}
}

func projectProvenance(p llm.MetadataProvenance) accountingProvenance {
	return accountingProvenance{Source: p.Source.String(), Snapshot: p.Snapshot, Revision: p.Revision, ObservedAt: p.ObservedAt, ExpiresAt: p.ExpiresAt}
}

func (w accountingV1) record() (RecordedProviderAccounting, error) {
	if w.Version != 1 {
		return RecordedProviderAccounting{}, ErrRecordedProviderAccountingVersion
	}
	if w.RequestBatchID == "" || w.TaskID == "" || w.Attempt <= 0 ||
		!accountingText(w.RequestBatchID, w.TaskID, w.Provider, w.Model) {
		return RecordedProviderAccounting{}, ErrInvalidRecordedProviderAccounting
	}
	r, err := w.evidenceRecord()
	if err != nil {
		return RecordedProviderAccounting{}, err
	}
	r.RequestBatchID, r.TaskID, r.Attempt = w.RequestBatchID, w.TaskID, w.Attempt
	return r, nil
}

// evidenceRecord validates only the common frozen evidence, never root identity
// or version. Each codec owns its independent identity and version boundary.
func (w accountingV1) evidenceRecord() (RecordedProviderAccounting, error) {
	availability, err := accountingwire.ParseEstimateAvailability(w.EstimateAvailability)
	if err != nil || availability.String() != w.EstimateAvailability ||
		availability != accountingAvailability(w.RateCard != nil, w.PricingUsage != nil, w.ConfiguredEstimate != nil) {
		return RecordedProviderAccounting{}, ErrInvalidRecordedProviderAccounting
	}
	r := RecordedProviderAccounting{
		Provider: w.Provider, Model: w.Model,
	}
	if w.Usage != nil {
		u, err := w.Usage.usage()
		if err != nil {
			return RecordedProviderAccounting{}, err
		}
		r.Usage = &u
	}
	if w.PricingUsage != nil {
		u, err := w.PricingUsage.usage()
		if err != nil {
			return RecordedProviderAccounting{}, err
		}
		r.PricingUsage = &u
	}
	if w.RateCard != nil {
		c, err := w.RateCard.card()
		if err != nil {
			return RecordedProviderAccounting{}, err
		}
		r.RateCard = &c
	}
	if w.ConfiguredEstimate != nil {
		e, err := w.ConfiguredEstimate.estimate()
		if err != nil {
			return RecordedProviderAccounting{}, err
		}
		r.ConfiguredEstimate = &e
	}
	if c := w.ReportedCost; c != nil {
		if !accountingAmount(c.Amount) || !accountingText(c.Currency) {
			return RecordedProviderAccounting{}, ErrInvalidRecordedProviderAccounting
		}
		r.ReportedCost = &llm.ReportedCost{Currency: llm.Currency(c.Currency), Reported: c.Reported, CoverageKnown: c.CoverageKnown}
		if c.Reported {
			r.ReportedCost.Amount = c.Amount
		}
	}
	return r, nil
}

func (w accountingUsage) usage() (llm.Usage, error) {
	counts := []int{w.PromptTokens, w.CompletionTokens, w.TotalTokens, w.CachedTokens,
		w.CacheCreationTokens, w.CacheCreation5mTokens, w.CacheCreation1hTokens}
	for _, count := range counts {
		if count < 0 {
			return llm.Usage{}, ErrInvalidRecordedProviderAccounting
		}
	}
	u := llm.Usage{
		PromptTokens: w.PromptTokens, CompletionTokens: w.CompletionTokens, TotalTokens: w.TotalTokens,
		CachedTokens: w.CachedTokens, CacheCreationTokens: w.CacheCreationTokens,
		CacheCreation5mTokens: w.CacheCreation5mTokens, CacheCreation1hTokens: w.CacheCreation1hTokens,
	}
	if w.ReasoningTokens != nil {
		if *w.ReasoningTokens < 0 {
			return llm.Usage{}, ErrInvalidRecordedProviderAccounting
		}
		u.ReasoningTokens, u.ReasoningTokensReported = *w.ReasoningTokens, true
	}
	return u, nil
}

func (w accountingPricing) usage() (llm.PricingUsage, error) {
	quantities := []*int64{w.Input.Total, w.Input.CacheRead, w.Input.CacheWrite, w.Input.Other, w.Output.Total, w.Output.Other}
	for _, quantity := range quantities {
		if quantity != nil && *quantity < 0 {
			return llm.PricingUsage{}, ErrInvalidRecordedProviderAccounting
		}
	}
	u := llm.PricingUsage{
		Input: llm.PricingInputUsage{
			Total: accountingQuantity(w.Input.Total), CacheRead: accountingQuantity(w.Input.CacheRead),
			CacheWrite: accountingQuantity(w.Input.CacheWrite), Other: accountingQuantity(w.Input.Other), PartitionKnown: w.Input.PartitionKnown,
		},
		Output:                    llm.PricingOutputUsage{Total: accountingQuantity(w.Output.Total), Other: accountingQuantity(w.Output.Other)},
		AdditionalChargesPossible: w.AdditionalChargesPossible,
	}
	if w.Input.WritesByLifetime != nil {
		u.Input.WritesByLifetime = make([]llm.CacheWriteQuantity, len(w.Input.WritesByLifetime))
		for i, row := range w.Input.WritesByLifetime {
			if row.Lifetime < 0 || (row.Quantity != nil && *row.Quantity < 0) {
				return llm.PricingUsage{}, ErrInvalidRecordedProviderAccounting
			}
			u.Input.WritesByLifetime[i] = llm.CacheWriteQuantity{Lifetime: time.Duration(row.Lifetime), Quantity: accountingQuantity(row.Quantity)}
		}
	}
	return u, nil
}

func accountingQuantity(n *int64) llm.TokenQuantity {
	if n == nil {
		return llm.TokenQuantity{}
	}
	return llm.TokenQuantity{Tokens: *n, Known: true}
}

func accountingTokenRate(n *float64) llm.TokenRate {
	if n == nil {
		return llm.TokenRate{}
	}
	return llm.TokenRate{PerMillionTokens: *n, Known: true}
}

func accountingAmount(n float64) bool {
	return !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0
}

func (w accountingReference) reference() llm.RateCardReference {
	return llm.RateCardReference{ID: w.ID, Revision: w.Revision, Entry: w.Entry}
}

func (w accountingProvenance) provenance() (llm.MetadataProvenance, error) {
	source, err := llm.ParseMetadataSource(w.Source)
	if err != nil || source.String() != w.Source || !accountingText(w.Snapshot, w.Revision) {
		return llm.MetadataProvenance{}, ErrInvalidRecordedProviderAccounting
	}
	return llm.MetadataProvenance{Source: source, Snapshot: w.Snapshot, Revision: w.Revision, ObservedAt: w.ObservedAt, ExpiresAt: w.ExpiresAt}, nil
}

func accountingBilling(s string) (llm.BillingMode, error) {
	billing, err := llm.ParseBillingMode(s)
	if err != nil || billing.String() != s {
		return llm.BillingMode{}, ErrInvalidRecordedProviderAccounting
	}
	return billing, nil
}

func (w accountingCard) card() (llm.RateCard, error) {
	if !accountingText(w.Reference.ID, w.Reference.Revision, w.Reference.Entry, w.PricingProvider, w.Currency) {
		return llm.RateCard{}, ErrInvalidRecordedProviderAccounting
	}
	billing, err := accountingBilling(w.Billing)
	if err != nil {
		return llm.RateCard{}, err
	}
	provenance, err := w.Provenance.provenance()
	if err != nil {
		return llm.RateCard{}, err
	}
	for _, rate := range []*float64{w.FreshInput, w.CacheRead, w.Output} {
		if rate != nil && !accountingAmount(*rate) {
			return llm.RateCard{}, ErrInvalidRecordedProviderAccounting
		}
	}
	c := llm.RateCard{
		Reference: w.Reference.reference(), PricingProvider: llm.PricingProviderID(w.PricingProvider), Currency: llm.Currency(w.Currency),
		Billing: billing, Provenance: provenance, Applicable: w.Applicable, TokenOnly: w.TokenOnly,
		FreshInput: accountingTokenRate(w.FreshInput), CacheRead: accountingTokenRate(w.CacheRead), Output: accountingTokenRate(w.Output),
	}
	if w.CacheWrites != nil {
		c.CacheWrites = make([]llm.CacheWriteRate, len(w.CacheWrites))
		for i, row := range w.CacheWrites {
			if row.Lifetime < 0 || (row.Rate != nil && !accountingAmount(*row.Rate)) {
				return llm.RateCard{}, ErrInvalidRecordedProviderAccounting
			}
			c.CacheWrites[i] = llm.CacheWriteRate{Lifetime: time.Duration(row.Lifetime), Rate: accountingTokenRate(row.Rate)}
		}
	}
	return c, nil
}

func (w accountingEstimate) estimate() (llm.PriceEstimate, error) {
	if !accountingText(w.Reference.ID, w.Reference.Revision, w.Reference.Entry, w.PricingProvider, w.Currency) {
		return llm.PriceEstimate{}, ErrInvalidRecordedProviderAccounting
	}
	billing, err := accountingBilling(w.Billing)
	if err != nil {
		return llm.PriceEstimate{}, err
	}
	provenance, err := w.Provenance.provenance()
	if err != nil {
		return llm.PriceEstimate{}, err
	}
	if !accountingAmount(w.InputAmount) || !accountingAmount(w.OutputAmount) || !accountingAmount(w.Subtotal) {
		return llm.PriceEstimate{}, ErrInvalidRecordedProviderAccounting
	}
	e := llm.PriceEstimate{
		Reference: w.Reference.reference(), PricingProvider: llm.PricingProviderID(w.PricingProvider), Currency: llm.Currency(w.Currency),
		Billing: billing, Provenance: provenance, HasKnownSubtotal: w.HasKnownSubtotal, Complete: w.Complete,
	}
	e.InputAmount, e.OutputAmount = w.InputAmount, w.OutputAmount
	if w.HasKnownSubtotal {
		e.Subtotal = w.Subtotal
	}
	if w.Issues != nil {
		e.Issues = make([]llm.PricingIssue, len(w.Issues))
		for i, issue := range w.Issues {
			kind, err := llm.ParsePricingIssueKind(issue.Kind)
			if err != nil || kind.String() != issue.Kind || issue.Lifetime < 0 || !accountingText(issue.Field) {
				return llm.PriceEstimate{}, ErrInvalidRecordedProviderAccounting
			}
			e.Issues[i] = llm.PricingIssue{Kind: kind, Field: issue.Field, Lifetime: time.Duration(issue.Lifetime)}
		}
	}
	return e, nil
}
