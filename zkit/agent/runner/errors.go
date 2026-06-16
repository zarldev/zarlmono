package runner

import "errors"

// Sentinel errors. Consumers can errors.Is them against TaskResult.Err
// (or the error returned from Run) to react to specific terminal states
// without parsing strings.
var (
	// ErrInvalidIterations is returned when TaskSpec.MaxIterations is
	// negative.
	ErrInvalidIterations = errors.New("runner: invalid max iterations")

	// ErrCancelled wraps the ctx.Err() when a Run was cancelled mid-loop
	// (typically by the ConversationLock yielding to a real-time
	// conversation that was cancelled in turn). Surfaced as
	// TaskResult.Err with Reason = TerminalCancelled.
	ErrCancelled = errors.New("runner: run cancelled")

	// ErrPromptRender wraps a PromptSource.System failure. Surfaced as
	// TaskResult.Err with Reason = TerminalError before iteration 0.
	ErrPromptRender = errors.New("runner: prompt render")

	// ErrCompact wraps a Compactor.Compact failure. Surfaced as
	// TaskResult.Err with Reason = TerminalError.
	ErrCompact = errors.New("runner: compact failed")

	// ErrIterationTimeout is the per-iteration timeout firing. Wraps
	// the streaming context's cancellation reason so consumers can
	// distinguish "we cut it off intentionally" from "the outer ctx
	// died". Surfaced as TaskResult.Err with Reason = TerminalError.
	ErrIterationTimeout = errors.New("runner: iteration timeout")

	// ErrStreamIdle is the stream-idle-timeout firing — the LLM
	// stopped emitting chunks for longer than the configured idle
	// budget. Wraps the underlying ctx cancel reason.
	ErrStreamIdle = errors.New("runner: stream idle timeout")

	// ErrThinkingBudget fires when an iteration emits only reasoning
	// (thinking) tokens past the configured byte budget without producing
	// any visible content or tool call — the degenerate "stuck thinking"
	// loop. Unlike the wall-clock timeouts it's content-aware, so it cuts
	// a runaway reasoning dump WITHOUT killing a healthy long generation
	// that streams real output. Recovered like an empty turn: the runner
	// injects a "stop reasoning, answer or call a tool" nudge and retries,
	// bounded by thinkingBudgetRecoverLimit.
	ErrThinkingBudget = errors.New("runner: thinking-only budget exceeded")

	// ErrEmptyStream is the terminal signal we synthesize when the
	// provider opened the completion stream cleanly (HTTP 200) but
	// closed it without emitting any content, tool call, or decodable
	// terminating frame — the SDK's SSE decoder surfaces this as an
	// EOF-class error (see isEmptyStreamDecodeError). DeepSeek's hosted
	// gateway does exactly this on heavy prefill: it accepts the
	// request, stalls past its first-token deadline on a large context,
	// then cuts the stream empty (observed as "stream: unexpected end
	// of JSON input" with zero content bytes). The failure is transient
	// — a retry, whose prefill the provider has usually cached, almost
	// always succeeds — so consumers can errors.Is this and retry
	// rather than treating it as a real terminal error.
	ErrEmptyStream = errors.New("runner: provider returned empty stream")

	// ErrUpstreamToolCallJSON is the soft-recoverable signal we
	// synthesize when the upstream LLM server rejects the model's
	// tool-call arguments as malformed JSON (llama-server's --jinja
	// path is the common offender — it validates tool-call args
	// server-side and returns 500 instead of letting our downstream
	// repair.Unmarshal recover them). The runner doesn't terminate
	// on this class of failure; it injects a corrective user message
	// asking the model to re-emit with proper escaping and continues.
	// Caps at runner.toolCallJSONRecoverLimit consecutive recoveries
	// per task to prevent looping on a model that can't produce
	// valid JSON at all.
	ErrUpstreamToolCallJSON = errors.New("runner: upstream rejected tool call args as malformed JSON")
)
