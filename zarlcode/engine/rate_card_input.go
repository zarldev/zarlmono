package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// MaxRateCardConfigBytes bounds the complete JSON input before decoding.
const MaxRateCardConfigBytes = 64 * 1024

const (
	maxRateCardIDBytes  = 1024
	maxRateCardURLBytes = 4096
)

// ErrInvalidRateCardConfig identifies rejected external rate-card input. Errors
// expose only static diagnostics, never decoder causes, field names or payloads.
var ErrInvalidRateCardConfig = errors.New("invalid rate-card configuration")

// ErrRateCardTargetMismatch identifies selection without an exact target match,
// including selection from an uninitialized configuration.
var ErrRateCardTargetMismatch = errors.New("rate-card target mismatch")

// RateCardTarget identifies the configured connection, effective requested model,
// exact base URL representation and actual selected frontend protocol. Provider is
// an open configured ID, not an adapter enum. An empty BaseURL denotes the
// registered default without a runtime URL override, never a wildcard. The host
// must resolve that default and the actual model/protocol before selection.
// These values carry no credentials and are never normalized by selection.
type RateCardTarget struct {
	Provider string
	Model    llm.ModelID
	BaseURL  string
	Protocol llm.ModelProtocol
}

// RateCardConfig owns one explicit local estimate policy and its exact target.
// Applicable and TokenOnly are operator assertions, not proof of platform billing
// or invoice coverage. Value copies borrow internal rates; Clone isolates them.
// Parsing and selection perform no I/O, clock reads, registration or inference.
type RateCardConfig struct {
	target RateCardTarget
	card   llm.RateCard
}

// Clone returns an independently owned policy snapshot.
func (c RateCardConfig) Clone() RateCardConfig {
	c.card = c.card.Clone()
	return c
}

// Select returns an independently owned card only for exact target equality.
// It never interprets aliases or substitutes protocols, URLs or legacy rates.
// The host must bind the result to a prepared construction and clone it per
// invocation; selection alone does not install or apply a pricing policy.
func (c RateCardConfig) Select(target RateCardTarget) (llm.RateCard, error) {
	if c.target.Provider == "" || c.target != target {
		return llm.RateCard{}, ErrRateCardTargetMismatch
	}
	return c.card.Clone(), nil
}

// ParseRateCardConfig parses wire v1 {version:1,target:{provider,model,base_url,
// protocol},card:{id,revision,entry,pricing_provider,currency,billing,applicable,
// token_only,...}}. All listed fields are required, including explicit booleans
// and base_url. Optional fresh_input_per_million, cache_read_per_million and
// output_per_million rates distinguish absent/null from known zero. Optional
// cache_writes is an array of {lifetime_seconds,per_million}; seconds must be
// positive JSON integers and rates may be absent/null. Only canonical known
// protocols and token-metered billing are supported by this flat token policy.
// IDs are opaque nonempty UTF-8, at most 1024 bytes, without Unicode controls;
// spelling, including spaces, is preserved. Nonempty URLs are at most 4096 bytes,
// HTTP(S), without userinfo, query or fragment, and are preserved exactly.
// Unknown/duplicate fields, trailing JSON and invalid scalars are rejected.
// observedAt is the host's actual input-observation time. Provenance records the
// SHA256 hex digest of the exact bytes, independently of the card's own revision.
func ParseRateCardConfig(data []byte, observedAt time.Time) (RateCardConfig, error) {
	if len(data) > MaxRateCardConfigBytes || !utf8.Valid(data) {
		return RateCardConfig{}, invalidRateCard("input size or encoding")
	}
	root, err := rateCardObject(data, "version", "target", "card")
	if err != nil {
		return RateCardConfig{}, err
	}
	var version int
	if json.Unmarshal(root["version"], &version) != nil || version != 1 {
		return RateCardConfig{}, invalidRateCard("unsupported version")
	}
	target, err := parseRateCardTarget(root["target"])
	if err != nil {
		return RateCardConfig{}, err
	}
	card, err := parseConfiguredRateCard(root["card"])
	if err != nil {
		return RateCardConfig{}, err
	}
	digest := sha256.Sum256(data)
	card.Provenance = llm.MetadataProvenance{
		Source:   llm.MetadataSources.METADATACONFIGURATION,
		Revision: hex.EncodeToString(digest[:]), ObservedAt: observedAt,
	}
	return RateCardConfig{target: target, card: card}, nil
}

func invalidRateCard(detail string) error {
	// All callers supply static, repository-owned text. Raw parser/URL errors
	// deliberately do not enter this chain, even as non-wrapped diagnostics.
	return fmt.Errorf("%w: %s", ErrInvalidRateCardConfig, detail)
}

func rateCardObject(data []byte, fields ...string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return nil, invalidRateCard("expected object")
	}
	object := make(map[string]json.RawMessage, len(fields))
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, invalidRateCard("invalid object")
		}
		key, ok := token.(string)
		if !ok || !slices.Contains(fields, key) {
			return nil, invalidRateCard("unsupported field")
		}
		if _, exists := object[key]; exists {
			return nil, invalidRateCard("duplicate field")
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, invalidRateCard("invalid value")
		}
		object[key] = value
	}
	if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
		return nil, invalidRateCard("invalid object")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, invalidRateCard("trailing content")
	}
	return object, nil
}

func rateCardString(data []byte, limit int, allowEmpty bool) (string, error) {
	var value string
	if len(data) == 0 || data[0] != '"' || json.Unmarshal(data, &value) != nil || !rateCardUnicodeEscapes(data) ||
		(!allowEmpty && value == "") || len(value) > limit {
		return "", invalidRateCard("invalid identifier")
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", invalidRateCard("invalid identifier")
		}
	}
	return value, nil
}

// The legacy JSON decoder replaces unpaired UTF-16 surrogates with U+FFFD.
// Reject those representations instead of silently repairing opaque IDs.
func rateCardUnicodeEscapes(data []byte) bool {
	for i := 1; i < len(data)-1; i++ {
		if data[i] != '\\' {
			continue
		}
		i++
		if data[i] != 'u' {
			continue
		}
		value, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if value < 0xd800 || value > 0xdfff {
			continue
		}
		if value > 0xdbff || i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
			return false
		}
		low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return false
		}
		i += 6
	}
	return true
}

func parseRateCardTarget(data []byte) (RateCardTarget, error) {
	object, err := rateCardObject(data, "provider", "model", "base_url", "protocol")
	if err != nil {
		return RateCardTarget{}, err
	}
	provider, err := rateCardString(object["provider"], maxRateCardIDBytes, false)
	if err != nil {
		return RateCardTarget{}, err
	}
	model, err := rateCardString(object["model"], maxRateCardIDBytes, false)
	if err != nil {
		return RateCardTarget{}, err
	}
	baseURL, err := rateCardString(object["base_url"], maxRateCardURLBytes, true)
	if err != nil {
		return RateCardTarget{}, err
	}
	if baseURL != "" {
		parsed, err := url.Parse(baseURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
			parsed.Hostname() == "" || parsed.User != nil || strings.ContainsAny(baseURL, "?#") {
			return RateCardTarget{}, invalidRateCard("invalid base URL")
		}
	}
	protocolText, err := rateCardString(object["protocol"], maxRateCardIDBytes, false)
	if err != nil {
		return RateCardTarget{}, err
	}
	protocol, err := llm.ParseModelProtocol(protocolText)
	if err != nil || protocol == llm.ModelProtocols.PROTOCOLUNKNOWN || protocol.String() != protocolText {
		return RateCardTarget{}, invalidRateCard("unsupported protocol")
	}
	return RateCardTarget{Provider: provider, Model: llm.ModelID(model), BaseURL: baseURL, Protocol: protocol}, nil
}

func parseConfiguredRateCard(data []byte) (llm.RateCard, error) {
	object, err := rateCardObject(data, "id", "revision", "entry", "pricing_provider", "currency",
		"billing", "applicable", "token_only", "fresh_input_per_million", "cache_read_per_million",
		"output_per_million", "cache_writes")
	if err != nil {
		return llm.RateCard{}, err
	}
	var card llm.RateCard
	card.Reference.ID, err = rateCardString(object["id"], maxRateCardIDBytes, false)
	if err != nil {
		return llm.RateCard{}, err
	}
	card.Reference.Revision, err = rateCardString(object["revision"], maxRateCardIDBytes, false)
	if err != nil {
		return llm.RateCard{}, err
	}
	card.Reference.Entry, err = rateCardString(object["entry"], maxRateCardIDBytes, false)
	if err != nil {
		return llm.RateCard{}, err
	}
	pricingProvider, err := rateCardString(object["pricing_provider"], maxRateCardIDBytes, false)
	if err != nil {
		return llm.RateCard{}, err
	}
	currency, err := rateCardString(object["currency"], maxRateCardIDBytes, false)
	if err != nil {
		return llm.RateCard{}, err
	}
	billingText, err := rateCardString(object["billing"], maxRateCardIDBytes, false)
	if err != nil {
		return llm.RateCard{}, err
	}
	billing, err := llm.ParseBillingMode(billingText)
	if err != nil || billing != llm.BillingModes.BILLINGTOKENMETERED || billing.String() != billingText {
		return llm.RateCard{}, invalidRateCard("unsupported billing")
	}
	card.PricingProvider, card.Currency, card.Billing = llm.PricingProviderID(pricingProvider), llm.Currency(currency), billing
	card.Applicable, err = rateCardAssertion(object["applicable"])
	if err != nil {
		return llm.RateCard{}, err
	}
	card.TokenOnly, err = rateCardAssertion(object["token_only"])
	if err != nil {
		return llm.RateCard{}, err
	}
	card.FreshInput, err = rateCardRate(object["fresh_input_per_million"])
	if err != nil {
		return llm.RateCard{}, err
	}
	card.CacheRead, err = rateCardRate(object["cache_read_per_million"])
	if err != nil {
		return llm.RateCard{}, err
	}
	card.Output, err = rateCardRate(object["output_per_million"])
	if err != nil {
		return llm.RateCard{}, err
	}
	card.CacheWrites, err = rateCardWrites(object["cache_writes"])
	if err != nil {
		return llm.RateCard{}, err
	}
	return card, nil
}

func rateCardAssertion(data []byte) (bool, error) {
	switch string(data) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, invalidRateCard("explicit boolean required")
	}
}

func rateCardRate(data []byte) (llm.TokenRate, error) {
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		return llm.TokenRate{}, nil
	}
	var rate float64
	if json.Unmarshal(data, &rate) != nil || math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 {
		return llm.TokenRate{}, invalidRateCard("invalid token rate")
	}
	if rate == 0 {
		// Float parsing rounds sufficiently small nonzero decimals to zero.
		// Inspect only the mantissa: exponent digits do not make zero nonzero.
		mantissa, _, _ := strings.Cut(strings.ToLower(string(data)), "e")
		if strings.ContainsAny(mantissa, "123456789") {
			return llm.TokenRate{}, invalidRateCard("token rate underflow")
		}
	}
	return llm.TokenRate{PerMillionTokens: rate, Known: true}, nil
}

func rateCardWrites(data []byte) ([]llm.CacheWriteRate, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var rows []json.RawMessage
	if data[0] != '[' || json.Unmarshal(data, &rows) != nil {
		return nil, invalidRateCard("invalid cache-write array")
	}
	rates := make([]llm.CacheWriteRate, 0, len(rows))
	seen := make(map[time.Duration]struct{}, len(rows))
	for _, row := range rows {
		object, err := rateCardObject(row, "lifetime_seconds", "per_million")
		if err != nil {
			return nil, err
		}
		var seconds int64
		if json.Unmarshal(object["lifetime_seconds"], &seconds) != nil || seconds <= 0 || seconds > math.MaxInt64/int64(time.Second) {
			return nil, invalidRateCard("invalid cache-write lifetime")
		}
		lifetime := time.Duration(seconds) * time.Second
		if _, exists := seen[lifetime]; exists {
			return nil, invalidRateCard("duplicate cache-write lifetime")
		}
		seen[lifetime] = struct{}{}
		rate, err := rateCardRate(object["per_million"])
		if err != nil {
			return nil, err
		}
		rates = append(rates, llm.CacheWriteRate{Lifetime: lifetime, Rate: rate})
	}
	return rates, nil
}
