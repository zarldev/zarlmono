package litellm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

const maxModelInfoBytes = 8 << 20

// ErrModelInfoUnavailable means optional ordinary-key enrichment was denied or
// not found (403/404). It does not classify the key as invalid, affect Models or
// completion, or imply that an administrative credential should be requested.
var ErrModelInfoUnavailable = errors.New("litellm model info unavailable")

// ErrModelInfoResponse identifies malformed, oversized, or invalid enrichment.
// Diagnostics never retain remote payloads, URLs, headers, or parser errors.
var ErrModelInfoResponse = errors.New("invalid litellm model info response")

// ModelInfoSnapshot is optional gateway-declared deployment evidence, not a
// resolved descriptor, authorization decision, or selected rate card. Provenance
// records the actual observation time and SHA256 of the complete response bytes.
// Provider.ModelInfo leaves expiry unset; ModelSource supplies its TTL deadline.
// Rows and their nested slices are caller-owned; value copies borrow them.
// Clone makes an independent snapshot. No raw gateway payload is retained.
type ModelInfoSnapshot struct {
	Rows       []ModelInfoRow
	Provenance llm.MetadataProvenance
}

// Clone returns independently owned rows and nested collections.
func (s ModelInfoSnapshot) Clone() ModelInfoSnapshot {
	s.Rows = slices.Clone(s.Rows)
	for i := range s.Rows {
		s.Rows[i].SupportedProtocols = slices.Clone(s.Rows[i].SupportedProtocols)
	}
	return s
}

// ModelInfoRow keeps a requested opaque alias, declared litellm_params.model,
// deployment id, and optional declared pricing identities separate. UpstreamModel
// and BaseModel are not established canonical identities or serving-provider
// assertions; neither they nor aliases are slash-split or canonicalized.
//
// SupportedProtocols contains only recognized declared model_info endpoints.
// Nil means absent/null; an empty slice means no recognized endpoints in an
// explicit list. This is not effective gateway support or native replay fidelity.
// Limits contains independent positive input/output ceilings, not enforcement
// evidence. ContextTokens stays unknown: max_tokens has no universal context
// semantics. No missing limit is derived from another.
//
// PricingOverrides records which of the four represented price fields are
// overridden. Known distinguishes an explicit list (including an empty list)
// from an absent or null list. Unknown field names are discarded. These flags
// are provenance, not rate applicability.
type ModelInfoRow struct {
	RequestedModel     llm.ModelID
	UpstreamModel      llm.ModelID
	Deployment         llm.DeploymentID
	BaseModel          llm.ModelID
	PricingProvider    llm.PricingProviderID
	SupportedProtocols []llm.ModelProtocol
	Limits             llm.ModelLimits
	Prices             ModelInfoPrices
	PricingOverrides   ModelInfoPricingOverrides
}

// ModelInfoPricingOverrides records the recognized fields in an explicit pricing_overrides list.
type ModelInfoPricingOverrides struct {
	Input      bool
	Output     bool
	CacheRead  bool
	CacheWrite bool
	Known      bool
}

// ModelInfoPrices retains the four baseline token prices, explicitly converted
// from LiteLLM's per-token units to llm.TokenRate's per-million-token units.
// Missing/null is unknown; explicit zero is known. These values assert neither
// currency, cache-write lifetime, account/tier applicability, nor bill coverage.
// They are not a selected RateCard; other billing dimensions may exist.
type ModelInfoPrices struct {
	Input      llm.TokenRate
	Output     llm.TokenRate
	CacheRead  llm.TokenRate
	CacheWrite llm.TokenRate
}

// ModelInfo explicitly fetches optional /v1/model/info using this provider's
// configured ordinary key and origin-bound sanitized transport. It never calls
// Models, changes completion behavior, requests master credentials, or caches.
// 403/404 return ErrModelInfoUnavailable, distinct from authentication failure.
// Other HTTP errors retain sanitized status classifications. Caller cancellation
// causes are preserved. Successful bodies are bounded to 8 MiB and always closed.
//
// The usual data array and tagged single-model data object are accepted.
// Rows require nonempty non-control Unicode aliases, upstream models and string
// deployment ids. Malformed UTF-8/unpaired JSON surrogates are rejected, not
// replaced. Rows, including repeated aliases/ids, are preserved in wire order.
// Negative, nonfinite, unrepresentable or wrong-type prices invalidate the whole
// snapshot; zero/null/nonpositive ceilings mean unknown, while positive ceilings
// must be exact int64 integers. Unknown JSON fields are ignored, never returned.
func (p *Provider) ModelInfo(ctx context.Context) (ModelInfoSnapshot, error) {
	if cause := context.Cause(ctx); cause != nil {
		return ModelInfoSnapshot{}, cause
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.baseURL, "/")+"/v1/model/info", nil)
	if err != nil {
		return ModelInfoSnapshot{}, errors.New("litellm model info: invalid gateway URL")
	}
	req.Header.Set("Accept", "application/json")
	response, err := p.client(llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS).Do(req)
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return ModelInfoSnapshot{}, cause
		}
		if status, ok := errors.AsType[*HTTPError](err); ok && (status.StatusCode == http.StatusForbidden || status.StatusCode == http.StatusNotFound) {
			return ModelInfoSnapshot{}, ErrModelInfoUnavailable
		}
		return ModelInfoSnapshot{}, fmt.Errorf("litellm model info: %w", completionError(ctx, err))
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxModelInfoBytes+1))
	if cause := context.Cause(ctx); cause != nil {
		return ModelInfoSnapshot{}, cause
	}
	if err != nil || len(data) > maxModelInfoBytes || !utf8.Valid(data) {
		return ModelInfoSnapshot{}, ErrModelInfoResponse
	}
	observedAt := time.Now().UTC()
	rows, err := parseModelInfo(ctx, data)
	if err != nil {
		return ModelInfoSnapshot{}, err
	}
	revision := sha256.Sum256(data)
	return ModelInfoSnapshot{Rows: rows, Provenance: llm.MetadataProvenance{
		Source: llm.MetadataSources.METADATADISCOVERY, Snapshot: "litellm-model-info-v1", Revision: hex.EncodeToString(revision[:]), ObservedAt: observedAt,
	}}, nil
}

// Wire fields are allowlisted from v1.109.0 types/router.py (LiteLLM_Params,
// ModelInfo), types/utils.py (ModelInfoBase), and proxy/proxy_server.py's
// _enrich_model_info_with_litellm_data. Do not retain litellm_params itself:
// additional credential/configuration fields are intentionally discarded.
type modelInfoWireRow struct {
	ModelName modelAlias `json:"model_name"`
	Params    struct {
		Model modelAlias `json:"model"`
	} `json:"litellm_params"`
	Info struct {
		ID                 modelAlias       `json:"id"`
		BaseModel          *modelAlias      `json:"base_model"`
		PricingProvider    *modelAlias      `json:"pricing_provider"`
		SupportedEndpoints []modelAlias     `json:"supported_endpoints"`
		MaxInputTokens     modelInfoCeiling `json:"max_input_tokens"`
		MaxOutputTokens    modelInfoCeiling `json:"max_output_tokens"`
		Input              modelInfoRate    `json:"input_cost_per_token"`
		Output             modelInfoRate    `json:"output_cost_per_token"`
		CacheRead          modelInfoRate    `json:"cache_read_input_token_cost"`
		CacheWrite         modelInfoRate    `json:"cache_creation_input_token_cost"`
		PricingOverrides   []string         `json:"pricing_overrides"`
	} `json:"model_info"`
}

func parseModelInfo(ctx context.Context, data []byte) ([]ModelInfoRow, error) {
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		return nil, ErrModelInfoResponse
	}
	payload := bytes.TrimSpace(envelope.Data)
	if len(payload) == 0 {
		return nil, ErrModelInfoResponse
	}
	var wireRows []modelInfoWireRow
	switch payload[0] {
	case '[':
		if json.Unmarshal(payload, &wireRows) != nil {
			return nil, ErrModelInfoResponse
		}
	case '{':
		var row modelInfoWireRow
		if json.Unmarshal(payload, &row) != nil {
			return nil, ErrModelInfoResponse
		}
		wireRows = []modelInfoWireRow{row}
	default:
		return nil, ErrModelInfoResponse
	}
	if cause := context.Cause(ctx); cause != nil {
		return nil, cause
	}
	rows := make([]ModelInfoRow, 0, len(wireRows))
	for _, wire := range wireRows {
		if cause := context.Cause(ctx); cause != nil {
			return nil, cause
		}
		row, err := wire.row()
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (w modelInfoWireRow) row() (ModelInfoRow, error) {
	for _, id := range []modelAlias{w.ModelName, w.Params.Model, w.Info.ID} {
		if !validInfoIdentity(id) {
			return ModelInfoRow{}, ErrModelInfoResponse
		}
	}
	row := ModelInfoRow{
		RequestedModel: llm.ModelID(w.ModelName), UpstreamModel: llm.ModelID(w.Params.Model), Deployment: llm.DeploymentID(w.Info.ID),
		Limits: llm.ModelLimits{InputTokens: int64(w.Info.MaxInputTokens), OutputTokens: int64(w.Info.MaxOutputTokens)},
		Prices: ModelInfoPrices{Input: w.Info.Input.rate, Output: w.Info.Output.rate, CacheRead: w.Info.CacheRead.rate, CacheWrite: w.Info.CacheWrite.rate},
	}
	if w.Info.BaseModel != nil {
		if !validInfoIdentity(*w.Info.BaseModel) {
			return ModelInfoRow{}, ErrModelInfoResponse
		}
		row.BaseModel = llm.ModelID(*w.Info.BaseModel)
	}
	if w.Info.PricingProvider != nil {
		if !validInfoIdentity(*w.Info.PricingProvider) {
			return ModelInfoRow{}, ErrModelInfoResponse
		}
		row.PricingProvider = llm.PricingProviderID(*w.Info.PricingProvider)
	}
	protocols, err := infoProtocols(w.Info.SupportedEndpoints)
	if err != nil {
		return ModelInfoRow{}, err
	}
	row.SupportedProtocols = protocols
	if w.Info.PricingOverrides != nil {
		row.PricingOverrides.Known = true
		for _, field := range w.Info.PricingOverrides {
			switch field {
			case "input_cost_per_token":
				row.PricingOverrides.Input = true
			case "output_cost_per_token":
				row.PricingOverrides.Output = true
			case "cache_read_input_token_cost":
				row.PricingOverrides.CacheRead = true
			case "cache_creation_input_token_cost":
				row.PricingOverrides.CacheWrite = true
			}
		}
	}
	return row, nil
}

func validInfoIdentity(id modelAlias) bool {
	return strings.TrimSpace(string(id)) != "" && !strings.ContainsFunc(string(id), unicode.IsControl)
}

func infoProtocols(endpoints []modelAlias) ([]llm.ModelProtocol, error) {
	if endpoints == nil {
		return nil, nil
	}
	protocols := make([]llm.ModelProtocol, 0, 4)
	for _, endpoint := range endpoints {
		if !validInfoIdentity(endpoint) {
			return nil, ErrModelInfoResponse
		}
		// Exact tagged endpoint vocabulary, not fuzzy suffix/substring matching.
		// types/utils.py also lists the generateContent model-template spelling.
		var protocol llm.ModelProtocol
		switch endpoint {
		case "/v1/chat/completions":
			protocol = llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS
		case "/v1/responses":
			protocol = llm.ModelProtocols.PROTOCOLRESPONSES
		case "/v1/messages":
			protocol = llm.ModelProtocols.PROTOCOLMESSAGES
		case "/v1beta/models/{model}:generateContent":
			protocol = llm.ModelProtocols.PROTOCOLGENERATECONTENT
		default:
			continue
		}
		if !slices.Contains(protocols, protocol) {
			protocols = append(protocols, protocol)
		}
	}
	return protocols, nil
}

type modelInfoRate struct{ rate llm.TokenRate }

func (r *modelInfoRate) UnmarshalJSON(data []byte) error {
	var value *float64
	if json.Unmarshal(data, &value) != nil {
		return ErrModelInfoResponse
	}
	if value == nil {
		return nil
	}
	scaled := *value * 1_000_000
	if *value < 0 || math.IsNaN(scaled) || math.IsInf(scaled, 0) || (*value == 0 && nonzeroMantissa(data)) {
		return ErrModelInfoResponse
	}
	r.rate = llm.TokenRate{PerMillionTokens: scaled, Known: true}
	return nil
}

// Nonzero literals that underflow float64 must not become reported free prices.
func nonzeroMantissa(data []byte) bool {
	for _, b := range data {
		if b == 'e' || b == 'E' {
			break
		}
		if b >= '1' && b <= '9' {
			return true
		}
	}
	return false
}

type modelInfoCeiling int64

func (c *modelInfoCeiling) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	// Bound exact decimal parsing separately. The float precheck rejects huge
	// exponents before big.Rat can allocate in proportion to an exponent.
	if len(data) > 64 {
		return ErrModelInfoResponse
	}
	approximate, err := strconv.ParseFloat(string(data), 64)
	if err != nil || math.IsNaN(approximate) || math.IsInf(approximate, 0) {
		return ErrModelInfoResponse
	}
	if approximate == 0 && nonzeroMantissa(data) {
		return ErrModelInfoResponse
	}
	if approximate <= 0 {
		return nil
	}
	if approximate > 0x1p63 {
		return ErrModelInfoResponse
	}
	exact, ok := new(big.Rat).SetString(string(data))
	if !ok || !exact.IsInt() || !exact.Num().IsInt64() {
		return ErrModelInfoResponse
	}
	*c = modelInfoCeiling(exact.Num().Int64())
	return nil
}
