package runner

import (
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/options"
)

// TurnQuality inspects an assistant turn (the finalised content +
// the structured tool calls extracted from the stream) and decides
// whether the turn is degenerate enough that the loop should inject
// a synthetic follow-up message instead of treating "no tool calls"
// as a clean terminal state.
//
// The default detector (EmptyResponseDetector) catches the small-
// model failure mode where thinking fills max_tokens and leaves no
// real reply — without this, the runner exits with a successful but
// content-less TaskResult and the task quietly stalls. Consumers
// with richer quality signals can swap in their own implementation
// via WithTurnQuality.
//
// Inspect returns a decision with a non-empty Correction when the
// runner should NOT exit on this turn; the runner appends the
// correction as a user message and continues the loop. Returning a
// zero decision lets the loop proceed normally (dispatch tool calls
// if any, exit if none). A decision may also request small next-
// iteration policy changes, such as disabling thinking for the retry.
//
// Inspect runs only when the assistant turn produced zero structured
// tool calls — the dispatch path already covers turns with tools.
// The hook receives the post-thinking-stripped content (i.e. what
// the user would see) and the tool-call slice for context, even
// though a non-empty slice short-circuits the check upstream.
//
// Implementations must be safe for concurrent use — a Runner is
// reusable across concurrent Runs.
type TurnQuality interface {
	Inspect(content string, toolCalls []llm.ToolCall) TurnQualityDecision
}

// TurnQualityDecision is the runner-side action requested by a
// TurnQuality hook. Correction is the user-side message to inject. If
// DisableThinking is true, the next and subsequent iterations in this
// Run use spec.Thinking=false; that mirrors the recovery needed for
// models that consumed their whole budget in the reasoning channel.
// MaxCorrections caps how many times this quality hook may inject a
// correction during one Run; zero means unlimited and preserves the
// original max-iterations-bounded behaviour.
type TurnQualityDecision struct {
	Correction      string
	DisableThinking bool
	MaxCorrections  int
}

// EmptyResponseDetector is the default TurnQuality implementation.
// It returns a "please make progress" correction when both the
// content and tool-call slice are empty after thinking is stripped
// — the precise shape that lets a thinking-budget-capped Qwen turn
// terminate the loop with nothing to show for it.
//
// The zero value is usable; the empty struct exists so an option
// caller can install or override the detector by type.
type EmptyResponseDetector struct {
	// Message is the correction surfaced to the model. Zero value
	// uses defaultEmptyResponseMessage — a generic "make progress"
	// nudge. Override when a specific consumer wants stricter
	// wording (e.g. "produce Answer: <value>" for benchmarks).
	Message string

	// DisableThinkingOnRetry asks the runner to turn off thinking for
	// the correction iteration. Useful for thinking models that emitted
	// only reasoning_content and no visible answer.
	DisableThinkingOnRetry bool

	// MaxCorrections caps detector injections per Run. Zero leaves the
	// retry bounded only by the runner's MaxIterations.
	MaxCorrections int
}

const defaultEmptyResponseMessage = "Your previous response was empty — no text and no tool call. " +
	"Either commit to an answer in plain text or call a tool to make progress. " +
	"An empty turn produces no work; the iteration cap is the only thing that stops the loop."

// Inspect implements TurnQuality. Returns the configured correction
// when the assistant produced neither content nor tool calls;
// otherwise returns a zero decision so the runner proceeds with its
// normal dispatch/exit branching.
func (d EmptyResponseDetector) Inspect(content string, toolCalls []llm.ToolCall) TurnQualityDecision {
	if strings.TrimSpace(content) != "" {
		return TurnQualityDecision{}
	}
	if len(toolCalls) > 0 {
		return TurnQualityDecision{}
	}
	msg := defaultEmptyResponseMessage
	if d.Message != "" {
		msg = d.Message
	}
	return TurnQualityDecision{
		Correction:      msg,
		DisableThinking: d.DisableThinkingOnRetry,
		MaxCorrections:  d.MaxCorrections,
	}
}

// WithTurnQuality installs the TurnQuality hook the runner consults
// at every iteration after the assistant message is finalised but
// before the dispatch / exit branching. A nil quality (the default)
// disables the check entirely, preserving the pre-C1 "no tool calls
// == exit" behaviour.
func WithTurnQuality(q TurnQuality) options.Option[Runner] {
	return func(r *Runner) { r.turnQuality = q }
}
