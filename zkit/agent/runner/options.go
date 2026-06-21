package runner

import (
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/templates"
	"github.com/zarldev/zarlmono/zkit/options"
)

// Options for runner.New, grouped by concern:
//
//   - Core wiring: Sink, ConversationLock, Prompt, Template
//   - Loop control: MaxIterations, ResultTruncator, ToolConcurrency, ProgressUpdater
//   - Timeouts: ToolTimeout, IterationTimeout, StreamIdleTimeout
//   - Compaction: Compactor, CompactKeepRecent, AdaptiveKeepRecent,
//     TokenPressureCompact
//   - Quality / steering: Steerer, TurnQuality, FinalizeWarn, ToolGate,
//     ContextBreakdown (defined beside their types in other files)
//
// All Options apply at construction (New) time. Anything per-task
// belongs on TaskSpec, not here.

// -- Core wiring --

// WithSink installs the event sink the runner publishes content,
// tool, and conversation lifecycle events to. The default is
// [StderrSink] (tool-progress lines on stderr); pass nil to
// silence the loop entirely for headless background tasks.
func WithSink(s EventSink) options.Option[Runner] {
	return func(r *Runner) { r.sink = s }
}

// WithConversationLock installs the cooperative yield mutex. When set,
// the runner waits for the lock to become inactive before each
// iteration so a real-time conversation gets LLM priority.
func WithConversationLock(l *ConversationLock) options.Option[Runner] {
	return func(r *Runner) { r.convLock = l }
}

// WithTools installs the ToolSource the runner snapshots each iteration
// for the LLM's tool list and dispatches against. Pull-based — the source
// is re-read every iteration, so tools registered mid-run (the agent built
// one with `register`, an MCP server just connected) become callable on
// the next turn. A nil source is ignored (the empty-registry default
// stands), so a runner with no WithTools is a valid tool-less agent.
func WithTools(source ToolSource) options.Option[Runner] {
	return func(r *Runner) {
		if source != nil {
			r.tools = source
		}
	}
}

// WithPrompt installs a PromptSource the runner consults at the top
// of every Run for the system message. Pull-based by design — the
// source is free to re-read its underlying state on each call so
// edits to a prompt file (or database row) take effect on the next
// turn without restarting the runner. Without this option the runner
// sends no system message at all.
//
// For the common case of a fixed prompt string, use [WithPromptText].
func WithPrompt(p PromptSource) options.Option[Runner] {
	return func(r *Runner) { r.prompt = p }
}

// WithPromptText is a shorthand for [WithPrompt]([StaticPrompt](prompt)).
func WithPromptText(prompt string) options.Option[Runner] {
	return WithPrompt(StaticPrompt(prompt))
}

// WithTemplate selects the chat template (Qwen3, Gemma4, etc.). The
// template handles per-model wire-format quirks — sentinel injection,
// thinking-mode kwargs, and so on — that local-model backends like
// Ollama need but managed APIs like Anthropic and OpenAI handle
// internally. The default is templates.Qwen3{}, picked because the
// runner originated against a local Qwen install; managed-API
// consumers can leave the default in place since their providers
// don't consult these template hooks.
func WithTemplate(t templates.ChatTemplate) options.Option[Runner] {
	return func(r *Runner) { r.template = t }
}

// -- Loop control --

// WithMaxIterations sets the default loop cap used when a TaskSpec's
// MaxIterations is zero. Default is 12.
func WithMaxIterations(n int) options.Option[Runner] {
	return func(r *Runner) {
		if n > 0 {
			r.maxIterations = n
		}
	}
}

// WithResultTruncator installs the policy for capping oversized tool
// results. Defaults to DefaultTruncator (trim only, no spill). The
// zarlcode installs SpillingTruncator so the agent can re-read
// the original transcript via bash.
func WithResultTruncator(t Truncator) options.Option[Runner] {
	return func(r *Runner) {
		if t != nil {
			r.truncator = t
		}
	}
}

// WithToolConcurrency caps how many tool calls in a single LLM
// tool-call batch the runner dispatches in parallel. n <= 1 disables
// parallelism entirely (sequential dispatch, the safe default).
// Default is 1.
func WithToolConcurrency(n int) options.Option[Runner] {
	return func(r *Runner) { r.toolConcurrency = n }
}

// WithProgressUpdater installs a callback the runner fires after every
// iteration's tool dispatch completes. The callback receives the
// just-completed iteration index and the cumulative tool-call count.
// Used to write intermediate progress to durable storage (eg. the
// headless_runs row) so a SIGKILL'd run still leaves a trail showing
// how far the agent got — without this, RunRecorder's CompleteHeadlessRun
// never runs and the row stays at its initial "iter=0/tools=0" state
// regardless of actual progress.
//
// The callback runs synchronously on the runner goroutine. Keep it
// fast — a single UPDATE statement or a channel send. Network calls
// will stall the iteration loop.
func WithProgressUpdater(u ProgressUpdater) options.Option[Runner] {
	return func(r *Runner) { r.progressUpdater = u }
}

// WithContextBreakdown enables the per-iteration per-role history tally on
// IterationCompleted.Context. It's an O(history) walk + allocation every
// iteration, so it's off by default — only a consumer that actually renders
// the breakdown (the TUI's context-window graph) should turn it on; a
// headless or eval run leaves it off and skips the work.
func WithContextBreakdown() options.Option[Runner] {
	return func(r *Runner) { r.contextBreakdown = true }
}

// -- Timeouts --

// WithToolTimeout caps a single tool dispatch's wall-clock budget.
// The default ([defaultToolTimeout], 5 minutes) is a balance: long
// enough for `go test ./...` on a non-trivial project to finish,
// short enough that a blocking dynamic / MCP tool can't wedge the
// run. Pass 0 to disable the per-tool cap entirely (the runner
// then trusts tools to honour ctx.Done — fine for trusted local
// tooling, not for arbitrary third-party MCP servers).
//
// Implementation note: the cap is applied as a context deadline
// around tool.Execute, and Execute runs in a goroutine so the runner
// can stop waiting when the deadline fires. Well-behaved tools see
// ctx.Done() fire and unwind cleanly. Tools that ignore context keep
// running past the deadline until they eventually return, but the
// runner records the timeout in the tool result and subsequent
// iterations continue unaffected.
func WithToolTimeout(d time.Duration) options.Option[Runner] {
	return func(r *Runner) {
		r.timeouts.tool = d
	}
}

// WithIterationTimeout caps the LLM call + stream drain of a single
// iteration. It does NOT bound tool dispatch — that's WithToolTimeout's
// job (see the timeouts type). The default is 5 minutes; pass 0 to disable.
// With a non-zero value, an iteration whose stream runs longer than d aborts
// cleanly with ErrIterationTimeout — far more diagnostic than the "signal:
// killed" an outer ctx timeout produces.
//
// Tune for the slowest legitimate stream you expect: a small local
// model stuck in thinking-mode shows up as "no progress after 60s +
// 10K tokens of streaming", which a 3-5 min cap recovers from without
// false-positive aborts on the genuinely-long-but-progressing cases
// (a 70-iter hugo refactor takes ~9 min total but each stream is ≤ a
// minute).
func WithIterationTimeout(d time.Duration) options.Option[Runner] {
	return func(r *Runner) {
		if d >= 0 {
			r.timeouts.iteration = d
		}
	}
}

// WithMaxTokens caps each completion request's output tokens (the wire
// max_tokens). It's a hard, deterministic ceiling on a single generation
// — unlike the wall-clock iteration timeout, it doesn't depend on the
// model honoring enable_thinking or on the timer goroutine being
// scheduled promptly under load. A value <= 0 leaves it unset (the
// provider/server default applies).
func WithMaxTokens(n int) options.Option[Runner] {
	return func(r *Runner) {
		if n > 0 {
			r.maxTokens = n
		}
	}
}

// WithTemperature sets the sampling temperature on each completion request.
// t <= 0 leaves it unset (the request omits temperature, so the provider /
// server default applies). A low value (e.g. 0.2) improves determinism and
// tool-call reliability for local models.
func WithTemperature(t float32) options.Option[Runner] {
	return func(r *Runner) {
		if t > 0 {
			r.temperature = t
		}
	}
}

// WithThinkingBudget cuts an iteration that has streamed only reasoning
// (thinking) tokens past byteBudget without yet emitting any visible
// content or tool call — the degenerate "stuck thinking" loop. The cut is
// recovered like an empty turn (a "stop reasoning, act now" nudge, then a
// retry) up to thinkingBudgetRecoverLimit. Being content-aware, it spares
// a healthy long generation that streams real output. A value <= 0
// disables the cut.
func WithThinkingBudget(byteBudget int) options.Option[Runner] {
	return func(r *Runner) {
		if byteBudget > 0 {
			r.thinkingBudgetBytes = byteBudget
		}
	}
}

// WithEmptyStreamBackoff sets the base pause before retrying an
// iteration that failed with [ErrEmptyStream] (the provider opened the
// stream then cut it empty). The pause doubles per consecutive retry,
// up to emptyStreamRetryLimit retries. The default is 500ms; pass 0
// for an immediate retry (used in tests). Unlike the timeout options
// this accepts 0 as a real value rather than a no-op.
func WithEmptyStreamBackoff(d time.Duration) options.Option[Runner] {
	return func(r *Runner) {
		if d >= 0 {
			r.emptyStreamBackoff = d
		}
	}
}

// WithStreamIdleTimeout caps the gap between consecutive chunks from
// the LLM stream. The default is 60 seconds; pass 0 to disable. Use to
// catch genuinely dead connections (provider hang) without bailing on
// legitimate long-running responses. Independent of iteration timeout:
// a stream that emits one chunk every 30s for 10 minutes is fine for
// idle timeout but trips iteration timeout.
func WithStreamIdleTimeout(d time.Duration) options.Option[Runner] {
	return func(r *Runner) {
		if d >= 0 {
			r.timeouts.streamIdle = d
		}
	}
}

// -- Compaction --

// WithCompactor installs a Compactor the runner consults at the start
// of every iteration after the first. Without this option the runner
// never auto-compacts; consumers handle context-window pressure at
// the REPL level (catch-and-retry, /compact slash command, etc.).
func WithCompactor(c compact.Compactor) options.Option[Runner] {
	return func(r *Runner) { r.compactor = c }
}

// WithCompactKeepRecent overrides the per-iteration compactor's
// keep-recent count. The compactor receives this on every Compact
// call (alongside the live message history); it represents the
// number of most-recent messages the engine must preserve verbatim.
// Default is 4 — conservative enough that a typical "last assistant
// turn + its tool results" window survives compaction. Bump for
// heavier-context workloads where the agent needs broader recent
// memory across compactions.
//
// Mutually exclusive with WithAdaptiveKeepRecent — last-write-wins.
func WithCompactKeepRecent(n int) options.Option[Runner] {
	return func(r *Runner) {
		if n > 0 {
			r.keep.static = n
			r.keep.adaptive = nil
		}
	}
}

// WithAdaptiveKeepRecent enables token-budget-aware keepRecent sizing.
// On every Compact call the runner walks the history tail-first,
// keeping messages until the running token estimate hits
// targetTokens, then clamps to [minKeep, maxKeep]. This solves the
// "static 4 messages" problem: a single huge tool result no longer
// dominates the keep window, and short narrative turns no longer
// starve the agent of recent memory.
//
// Reasonable defaults for a 32k-window model: (8000, 2, 12). Smaller
// windows want smaller targets. Pass minKeep=0 / maxKeep=0 to use
// safe defaults (2 / 20). targetTokens <= 0 falls back to the static
// static keep value (disables adaptive).
//
// Mutually exclusive with WithCompactKeepRecent — last-write-wins.
func WithAdaptiveKeepRecent(targetTokens, minKeep, maxKeep int) options.Option[Runner] {
	return func(r *Runner) {
		if targetTokens <= 0 {
			return
		}
		if minKeep <= 0 {
			minKeep = 2
		}
		if maxKeep <= 0 {
			maxKeep = 20
		}
		if maxKeep < minKeep {
			maxKeep = minKeep
		}
		r.keep.adaptive = func(h []llm.Message) int {
			return compact.AdaptiveKeepRecent(h, targetTokens, minKeep, maxKeep)
		}
	}
}

// WithTokenPressureCompact installs a force-compact threshold keyed to the
// provider's reported prompt-token usage. When the previous turn's
// PromptTokens ÷ budget ≥ fraction, the runner skips the Prober gate,
// shrinks keepRecent to 1, and calls Compact unconditionally.
//
// This complements (does not replace) the engine-side byte-pressure
// thresholds. Engines that estimate "no work to do" from raw bytes can
// underestimate real context cost — a 35B-class local model loses
// structured-output discipline around half its nominal window even when the
// bytes look fine. The provider's tokenizer is the only reliable signal that
// we've crossed the *effective* coherent window; this turns it into a trim.
//
// On force-compact the runner shrinks keepRecent to 1 — just the latest
// message survives — so the latest (often huge) tool result is itself
// eligible for the engine's most aggressive trim (Tiered's Phase-3
// placeholdering) on the next iteration. Trimming only the older slice while a
// fresh oversized result sits inside the keep window would leave net prompt
// size unchanged; dropping to 1 breaks that equilibrium. The assistant
// tool_call metadata pointing at the result survives, so the model still sees
// what it asked for.
//
// Two ways to express the trigger:
//   - window-relative: budget = the model's context window, fraction = the
//     share at which to compact. coderunner.StandardOptions wires this with a
//     shared fraction so the TUI and eval can't diverge.
//   - absolute: budget = the empirical token threshold, fraction = 1.0 — the
//     coherence wall is really a property of the *model*, not a percentage of
//     its window. Observed thresholds (nominal window → wall):
//     Qwen3.6-35B (131k) → ~65k    Llama-3.1-70B (128k) → ~75k
//     GPT-5.5 (200k)     → ~120k   Claude-4.6 Sonnet (1M) → ~250k
//
// budget ≤ 0 or fraction ≤ 0 disables the force-path; fraction is clamped to 1.0.
func WithTokenPressureCompact(budget int, fraction float64) options.Option[Runner] {
	return func(r *Runner) {
		if budget <= 0 || fraction <= 0 {
			return
		}
		if fraction > 1.0 {
			fraction = 1.0
		}
		r.keep.budget = budget
		r.keep.fraction = fraction
	}
}
