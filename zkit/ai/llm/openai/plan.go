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
}

func (responsesPlan) requestPlan() {}

// unsupportedRequestError is returned when the planner identifies a
// request combination that cannot be served by any supported endpoint.
type unsupportedRequestError struct {
	Model    string
	Feature  string
	Endpoint EndpointKind
	Hint     string
}

func (e *unsupportedRequestError) Error() string {
	ep := EndpointKinds.ENDPOINTCHATCOMPLETIONS.String()
	if e.Endpoint == EndpointKinds.ENDPOINTRESPONSES {
		ep = EndpointKinds.ENDPOINTRESPONSES.String()
	}
	return fmt.Sprintf("%s does not support %q on %s endpoint: %s",
		e.Model, e.Feature, ep, e.Hint)
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
		strings.HasPrefix(norm, "gpt-5")
}

// planRequest selects the endpoint and parameter plan for a given model
// and request. It is a pure function — no I/O, no provider state.
//
// Initial rules:
//
//	known reasoning/GPT-5 + tools          → responsesPlan
//	known reasoning/GPT-5, no tools        → chatCompletionPlan with max_completion_tokens
//	everything else                        → chatCompletionPlan with max_tokens
func planRequest(model string, req llm.CompletionRequest) (requestPlan, error) {
	if !knownReasoningModel(model) {
		return chatCompletionPlan{
			tokenLimit:   TokenLimitFields.TOKENLIMITMAXTOKENS,
			includeUsage: true,
		}, nil
	}

	// Known reasoning/GPT-5 family.
	if len(req.Tools) > 0 {
		// Tools require the Responses endpoint for known reasoning families.
		// Responses is currently streaming-only; reject non-stream requests.
		if !req.Stream {
			return nil, &unsupportedRequestError{
				Model:    model,
				Feature:  "non-stream tool request",
				Endpoint: EndpointKinds.ENDPOINTRESPONSES,
				Hint:     "tools on this model family require streaming Responses; set Stream: true or use a model that supports Chat Completions with tools",
			}
		}

		plan := responsesPlan{
			parallelToolCalls: true,
		}
		if req.Thinking.Enabled {
			med := ReasoningEfforts.REASONINGEFFORTMEDIUM
			plan.reasoning = &med
			plan.includeEncryptedReasoning = true
		}
		return plan, nil
	}

	return chatCompletionPlan{
		tokenLimit:   TokenLimitFields.TOKENLIMITMAXCOMPLETIONTOKENS,
		includeUsage: true,
	}, nil
}
