package openai

import (
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// requestPlan is the sealed interface for endpoint-specific plans.
// Only chatCompletionPlan and responsesPlan implement it.
type requestPlan interface {
	requestPlan()
}

// chatCompletionPlan carries the decisions specific to
// POST /v1/chat/completions.
type chatCompletionPlan struct {
	tokenLimit   TokenLimitField
	includeUsage bool
}

func (chatCompletionPlan) requestPlan() {}

// responsesPlan carries the decisions specific to POST /v1/responses.
type responsesPlan struct {
	reasoning                 *ReasoningEffort
	includeEncryptedReasoning bool
	parallelToolCalls         bool
	textVerbosity             string
	promptCacheKey            string
	promptCacheRetention      string
}

func (responsesPlan) requestPlan() {}

// unsupportedRequestError is returned when the planner identifies a request
// combination that cannot be served by any supported endpoint.
type unsupportedRequestError struct {
	Model    string
	Feature  string
	Endpoint EndpointKind
	Hint     string
}

func (e *unsupportedRequestError) Error() string {
	endpoint := EndpointKinds.ENDPOINTCHATCOMPLETIONS.String()
	if e.Endpoint == EndpointKinds.ENDPOINTRESPONSES {
		endpoint = EndpointKinds.ENDPOINTRESPONSES.String()
	}
	return fmt.Sprintf("%s does not support %q on %s endpoint: %s", e.Model, e.Feature, endpoint, e.Hint)
}

// knownReasoningModel reports whether the model ID belongs to a family
// known to require Responses for tool support and max_completion_tokens /
// max_output_tokens for the token cap. Normalization is local; the
// original model string is preserved on outbound requests.
func knownReasoningModel(m string) bool {
	norm := strings.ToLower(strings.TrimSpace(m))
	return strings.HasPrefix(norm, "o1") ||
		strings.HasPrefix(norm, "o3") ||
		strings.HasPrefix(norm, "o4") ||
		strings.HasPrefix(norm, "gpt-5") ||
		norm == modelGPT61Sol || norm == modelGPT6Astra || isGPT6SolOrLuna(norm)
}

func supportsCoreResponses(model string) bool {
	norm := strings.ToLower(strings.TrimSpace(model))
	return norm == modelGPT61Sol || norm == modelGPT6Astra || isGPT6SolOrLuna(norm) || norm == modelGPT56 || strings.HasPrefix(norm, modelGPT56+"-")
}

func isGPT6SolOrLuna(model string) bool {
	return model == modelGPT6Sol || model == modelGPT6Luna
}

func supportsReasoningEffort(model string, effort ReasoningEffort) bool {
	switch effort {
	case ReasoningEfforts.REASONINGEFFORTNONE:
		return isGPT6SolOrLuna(strings.ToLower(strings.TrimSpace(model)))
	case ReasoningEfforts.REASONINGEFFORTMAX:
		return supportsMaxReasoning(model)
	default:
		return true
	}
}

func supportsMaxReasoning(model string) bool {
	return supportsCoreResponses(model)
}

func requestOptionString(options llm.ModelOptions, key string) string {
	value, _ := options[key].(string)
	return strings.TrimSpace(value)
}

// requestServiceTier accepts only published speed tiers; omission preserves the
// project's default. Account/model eligibility is enforced by the backend.
func requestServiceTier(options llm.ModelOptions) string {
	tier := requestOptionString(options, "service_tier")
	switch tier {
	case "default", "fast", "priority", "ultrafast":
		return tier
	default:
		return ""
	}
}

// planRequest selects the endpoint and parameter plan for a given model
// and request. It is a pure function — no I/O, no provider state.
//
// Initial rules:
//
//	known reasoning/GPT-5 + tools          → responsesPlan
//	known reasoning/GPT-5, no tools        → chatCompletionPlan with max_completion_tokens
//	everything else                        → chatCompletionPlan with max_tokens
func planRequest(model string, req llm.CompletionRequest, responsesAPI bool, defaults responsesPlan) (requestPlan, error) {
	if supportsCoreResponses(model) && responsesAPI && req.Stream {
		plan := defaults
		if plan.reasoning != nil && *plan.reasoning == ReasoningEfforts.REASONINGEFFORTNONE && !supportsReasoningEffort(model, *plan.reasoning) {
			plan.reasoning = nil
		}
		plan.parallelToolCalls = len(req.Tools) > 0
		if req.Thinking.Enabled && plan.reasoning == nil {
			medium := ReasoningEfforts.REASONINGEFFORTMEDIUM
			plan.reasoning = &medium
		}
		if effort := requestOptionString(req.Options, "reasoning_effort"); effort != "" {
			parsed, err := ParseReasoningEffort(effort)
			if err == nil && supportsReasoningEffort(model, parsed) {
				plan.reasoning = &parsed
			}
		}
		if verbosity := requestOptionString(req.Options, "text_verbosity"); verbosity == textVerbosityLow || verbosity == textVerbosityMedium || verbosity == textVerbosityHigh {
			plan.textVerbosity = verbosity
		}
		if cacheKey := requestOptionString(req.Options, "prompt_cache_key"); cacheKey != "" {
			plan.promptCacheKey = cacheKey
		}
		if retention := requestOptionString(req.Options, "prompt_cache_retention"); retention == "in-memory" || retention == "24h" {
			plan.promptCacheRetention = retention
		}
		plan.includeEncryptedReasoning = plan.reasoning != nil || hasOpenAIContinuationItems(req.Messages)
		return plan, nil
	}

	if supportsCoreResponses(model) && len(req.Tools) > 0 && !req.Stream {
		return nil, &unsupportedRequestError{
			Model:    model,
			Feature:  "non-stream tool request",
			Endpoint: EndpointKinds.ENDPOINTRESPONSES,
			Hint:     "tools on this model family require streaming Responses; set Stream: true or use a model that supports Chat Completions with tools",
		}
	}
	if !knownReasoningModel(model) {
		return chatCompletionPlan{tokenLimit: TokenLimitFields.TOKENLIMITMAXTOKENS, includeUsage: true}, nil
	}
	return chatCompletionPlan{tokenLimit: TokenLimitFields.TOKENLIMITMAXCOMPLETIONTOKENS, includeUsage: true}, nil
}
