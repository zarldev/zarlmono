package runner

import (
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/toolparse"
	"github.com/zarldev/zarlmono/zkit/options"
)

// malformedToolCallCorrection is the user-side turn injected when the model
// tried to call a tool but emitted JSON the recovery pipeline could not parse
// (a misplaced or missing bracket, an unbalanced quote). It names the failure
// and asks for a clean re-emit rather than guessing the intended call —
// repairing arbitrary broken JSON in the parser risks dispatching a call the
// model never meant, so the model fixes its own output instead.
const malformedToolCallCorrection = "Your previous response looked like a tool call but the JSON was malformed, so no tool ran and the raw text leaked into the transcript. " +
	"Re-emit the call as a single valid JSON object: {\"tool_calls\":[{\"id\":\"call_1\",\"name\":\"<tool>\",\"arguments\":{...}}]} — " +
	"check every bracket and brace is balanced and closed in order, and escape any quotes or newlines inside string values. Try again now."

// MalformedToolCallDetector is a TurnQuality guardrail that catches a tool call
// the model emitted as text but malformed badly enough that neither the
// provider's own recovery nor the runner's text fallback could parse it — the
// turn arrives with visible content that is a tool-call artifact yet zero
// structured calls. Left unguarded the artifact leaks into the transcript as
// prose and the intended tool never runs. Inspect runs only on zero-tool-call
// turns (the runner gates it), and by that point the recovery pipeline has
// already tried and failed, so artifact-shaped content here is necessarily an
// unrecovered call. The detector injects one corrective turn asking the model
// to re-emit valid JSON, bounded by MaxCorrections.
//
// The zero value is usable.
type MalformedToolCallDetector struct {
	// Message overrides the correction surfaced to the model. Zero value uses
	// malformedToolCallCorrection.
	Message string
	// MaxCorrections caps detector injections per Run. Zero leaves the retry
	// bounded only by the runner's MaxIterations.
	MaxCorrections int
}

// Inspect implements TurnQuality. It returns a correction when the turn
// produced no tool calls but the visible content opens with a known tool-call
// artifact prefix — the signature of a malformed, unrecovered call. Any other
// turn yields a zero decision so the runner proceeds normally.
func (d MalformedToolCallDetector) Inspect(content string, toolCalls []llm.ToolCall) TurnQualityDecision {
	if len(toolCalls) > 0 {
		return TurnQualityDecision{}
	}
	if !toolparse.IsToolCallArtifactPrefix(content) {
		return TurnQualityDecision{}
	}
	// A prefix that the recovery pipeline could actually parse would have
	// produced tool calls upstream and skipped this hook; reaching here with an
	// artifact prefix means the parse failed. Confirm there is no recoverable
	// call so a future parser improvement can't make this fire spuriously.
	if res := toolparse.ParseArtifact(content); len(res.Calls) > 0 {
		return TurnQualityDecision{}
	}
	msg := malformedToolCallCorrection
	if d.Message != "" {
		msg = d.Message
	}
	return TurnQualityDecision{Correction: msg, MaxCorrections: d.MaxCorrections}
}

// ChainTurnQuality composes several TurnQuality detectors into one, returning
// the first non-empty decision in order. It lets a consumer stack independent
// quality guards (malformed-call recovery, empty-response recovery, …) behind
// the runner's single TurnQuality seam without one detector having to know
// about the others.
type ChainTurnQuality []TurnQuality

// Inspect runs each detector in order and returns the first that asks for a
// correction; a zero decision when none do.
func (c ChainTurnQuality) Inspect(content string, toolCalls []llm.ToolCall) TurnQualityDecision {
	for _, q := range c {
		if q == nil {
			continue
		}
		if decision := q.Inspect(content, toolCalls); decision.Correction != "" {
			return decision
		}
	}
	return TurnQualityDecision{}
}

// WithMalformedToolCallGuard is a convenience option that installs a
// MalformedToolCallDetector chained ahead of an existing TurnQuality hook,
// preserving whatever was already configured.
func WithMalformedToolCallGuard(d MalformedToolCallDetector) options.Option[Runner] {
	return func(r *Runner) {
		if r.turnQuality == nil {
			r.turnQuality = d
			return
		}
		r.turnQuality = ChainTurnQuality{d, r.turnQuality}
	}
}
