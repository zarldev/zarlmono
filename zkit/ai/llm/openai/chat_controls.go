package openai

import (
	"strings"

	"github.com/openai/openai-go/v3/option"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// chatControlOptions forwards explicit controls only for hosted Chat models.
func (p *Provider) chatControlOptions(req llm.CompletionRequest) []option.RequestOption {
	if p.customBaseURL {
		return nil
	}
	model := strings.ToLower(strings.TrimSpace(p.model))
	gpt5 := model == "gpt-5" || model == "gpt-5-mini" || model == "gpt-5-nano" || model == "gpt-5-2025-08-07" || model == "gpt-5-mini-2025-08-07" || model == "gpt-5-nano-2025-08-07"
	gpt6 := model == modelGPT61Sol || model == modelGPT6Astra || isGPT6SolOrLuna(model)
	if !gpt5 && !gpt6 {
		return nil
	}
	var opts []option.RequestOption
	effort := requestOptionString(req.Options, "reasoning_effort")
	allowed := gpt5 && (effort == "minimal" || effort == "low" || effort == "medium" || effort == "high")
	if gpt6 {
		parsed, err := ParseReasoningEffort(effort)
		allowed = err == nil && supportsReasoningEffort(model, parsed)
	}
	if allowed {
		opts = append(opts, option.WithJSONSet("reasoning_effort", effort))
	}
	if verbosity := requestOptionString(req.Options, "text_verbosity"); verbosity == textVerbosityLow || verbosity == textVerbosityMedium || verbosity == textVerbosityHigh {
		opts = append(opts, option.WithJSONSet("verbosity", verbosity))
	}
	return opts
}
