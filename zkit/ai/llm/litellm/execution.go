package litellm

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// ErrAffinityUnsupported identifies an affinity requirement that the gateway
// adapter cannot enforce. Complete returns it lazily before codec setup or I/O;
// sending a session hint is not verified server enforcement.
var ErrAffinityUnsupported = errors.New("litellm required affinity unsupported")

// executionState belongs to one stream invocation. Only bounded semantic values
// survive observe; no response header map or raw diagnostic is retained. The
// mutex keeps transport publication and consumption together without relying on
// a particular SDK's transport calling convention.
type executionState struct {
	model   string
	apiKey  string
	session llm.SessionID

	mu          sync.Mutex
	observation llm.ExecutionObservation
	pending     bool
}

func (s *executionState) observe(headers http.Header) {
	observation := llm.ExecutionObservation{
		RequestedModel:    llm.ModelID(executionID(s.model, s.apiKey)),
		GatewayCallID:     llm.CallID(executionID(singleHeader(headers, "X-Litellm-Call-Id"), s.apiKey)),
		GatewayDeployment: llm.DeploymentID(executionID(singleHeader(headers, "X-Litellm-Model-Id"), s.apiKey)),
		GatewayVersion:    llm.GatewayVersionID(executionID(singleHeader(headers, "X-Litellm-Version"), s.apiKey)),
		ReportedModel:     llm.ModelID(executionID(singleHeader(headers, "X-Litellm-Model-Name"), s.apiKey)),
		RetryCount:        executionCount(singleHeader(headers, "X-Litellm-Attempted-Retries"), s.apiKey),
		FallbackCount:     executionCount(singleHeader(headers, "X-Litellm-Attempted-Fallbacks"), s.apiKey),
		ReportedCost:      executionCost(singleHeader(headers, "X-Litellm-Response-Cost"), s.apiKey),
	}
	// Gateway runtime version is not a model version. Route, provider identity,
	// model version, affinity support and cache outcomes remain unknown.
	s.mu.Lock()
	s.observation = observation
	s.pending = true
	s.mu.Unlock()
}

func (s *executionState) take() (llm.ExecutionObservation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	observation, pending := s.observation, s.pending
	s.pending = false
	return observation, pending
}

// Ambiguous repeated values are unknown rather than arbitrarily choosing one.
func singleHeader(headers http.Header, name string) string {
	values := headers.Values(name)
	if len(values) != 1 {
		return ""
	}
	return values[0]
}

func executionID(raw, apiKey string) string {
	if len(raw) > llm.MaxExecutionIDBytes || !utf8.ValidString(raw) || containsKey(raw, apiKey) || strings.Contains(raw, "://") {
		return ""
	}
	for _, r := range raw {
		if unicode.IsControl(r) {
			return ""
		}
	}
	return strings.Clone(raw)
}

func containsKey(raw, apiKey string) bool {
	return apiKey != "" && strings.Contains(raw, apiKey)
}

func executionCount(raw, apiKey string) llm.ObservedCount {
	if raw == "" || len(raw) > llm.MaxExecutionIDBytes || containsKey(raw, apiKey) {
		return llm.ObservedCount{}
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return llm.ObservedCount{}
		}
	}
	count, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return llm.ObservedCount{}
	}
	return llm.ObservedCount{Count: count, Known: true}
}

func executionCost(raw, apiKey string) llm.ReportedCost {
	// Accept decimal floats with an optional sign and decimal exponent, not
	// Go hexadecimal literals or digit separators. ParseFloat rejects whitespace.
	if len(raw) > llm.MaxExecutionIDBytes || containsKey(raw, apiKey) || strings.ContainsAny(raw, "xX_") {
		return llm.ReportedCost{}
	}
	amount, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 || (amount == 0 && nonzeroMantissa([]byte(raw))) {
		return llm.ReportedCost{}
	}
	return llm.ReportedCost{Amount: amount, Reported: true, Source: "x-litellm-response-cost"}
}

// mergeBody retains the header identity snapshot while preferring a terminal
// body cost over the overlapping header cost. Source explicitly flags conflict;
// neither amounts nor repeated snapshots are added. Currency/coverage stay unknown.
func (s *executionState) mergeBody(body llm.ExecutionObservation) llm.ExecutionObservation {
	s.mu.Lock()
	defer s.mu.Unlock()
	header := s.observation
	observation := body
	s.pending = false
	if observation.Route == "" {
		observation.Route = header.Route
	}
	if observation.RequestedModel == "" {
		observation.RequestedModel = header.RequestedModel
	}
	if observation.GatewayDeployment == "" {
		observation.GatewayDeployment = header.GatewayDeployment
	}
	if observation.GatewayVersion == "" {
		observation.GatewayVersion = header.GatewayVersion
	}
	if observation.ReportedModel == "" {
		observation.ReportedModel = header.ReportedModel
	}
	if observation.ReportedVersion == "" {
		observation.ReportedVersion = header.ReportedVersion
	}
	if observation.GatewayCallID == "" {
		observation.GatewayCallID = header.GatewayCallID
	}
	if observation.ProviderCallID == "" {
		observation.ProviderCallID = header.ProviderCallID
	}
	if !observation.PromptCacheReadTokens.Known {
		observation.PromptCacheReadTokens = header.PromptCacheReadTokens
	}
	if !observation.PromptCacheWriteTokens.Known {
		observation.PromptCacheWriteTokens = header.PromptCacheWriteTokens
	}
	if !observation.RetryCount.Known {
		observation.RetryCount = header.RetryCount
	}
	if !observation.FallbackCount.Known {
		observation.FallbackCount = header.FallbackCount
	}
	if observation.AffinitySupport == llm.Supports.SUPPORTUNKNOWN {
		observation.AffinitySupport = header.AffinitySupport
	}
	if !body.ReportedCost.Reported {
		observation.ReportedCost = header.ReportedCost
	} else if header.ReportedCost.Reported && header.ReportedCost.Amount != body.ReportedCost.Amount {
		observation.ReportedCost.Source += "-conflicts-header"
	}
	return observation
}
