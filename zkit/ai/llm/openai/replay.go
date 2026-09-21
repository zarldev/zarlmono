package openai

// SupportsResponsesReplay reports whether the default provider routes streaming
// requests for model through Responses. Runtime capture must additionally verify
// the constructed provider with DefaultReplayCompatible.
func SupportsResponsesReplay(model string) bool {
	return supportsCoreResponses(model)
}

// DefaultReplayCompatible reports whether this constructed provider uses the
// stock endpoint and model's default streaming replay route. Custom endpoints,
// model mismatches and disabled Responses for a Responses model are excluded.
// It exposes no endpoint, credentials or other private configuration.
func (p *Provider) DefaultReplayCompatible(model string) bool {
	return !p.customBaseURL && p.model == model && (!supportsCoreResponses(model) || p.responsesAPI)
}
