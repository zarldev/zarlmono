// Package runner provides the canonical agent loop — think → call tools
// → observe → repeat — as a transport-agnostic, drop-anywhere library.
//
// Every concern is pushed onto a small consumer-implemented interface:
//
//   - [Client]       — LLM streaming completion.
//   - [ToolSource]   — the live tool list + dispatcher.
//   - [PromptSource] — system prompt resolution (live-reloadable).
//   - [EventSink]    — observability for content / tool / conversation /
//     steer / compaction events.
//   - [Steerer]      — queued user messages between iterations.
//   - [Truncator]    — tool-result trimming policy.
//   - [Compactor]    — conversation-history compaction policy.
//
// What lives in this package is the loop body plus the types those
// interfaces use ([TaskSpec], [TaskResult], event payloads, sentinel
// errors).
//
// All tools the LLM sees are normal registry tools — there is no
// "action tool" classification. The runner exposes whatever the
// installed ToolSource yields each iteration. Consumers that want
// sub-agent recursion register zkit/agent/tools/spawn.New as one of
// those tools; the runner ships none.
//
// Construction is options-driven via zkit/options:
//
//	r := runner.New(client,
//	    runner.WithTools(toolRegistry),
//	    runner.WithSink(sink),
//	    runner.WithPrompt(promptSource),
//	    runner.WithSteerer(steerer),
//	    runner.WithCompactor(compactor),
//	    runner.WithMaxIterations(20),
//	    runner.WithToolConcurrency(4),
//	)
//	result, err := r.Run(ctx, runner.TaskSpec{
//	    Prompt: "summarise today's news",
//	})
//
// The loop body lives in run.go; this file is types and construction.
package runner

import (
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/compact"
	"github.com/zarldev/zarlmono/zkit/ai/llm/templates"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/options"
)

// Runner is the agent loop. Construct with New and call Run for each
// task. The runner's own state (client, registries, stores) is
// read-only after construction; per-task state is local to Run, so
// concurrent Run calls do not corrupt each other.
//
// Concurrent Run calls do, however, share the installed plumbing —
// EventSink, Steerer, Truncator, PromptSource. Each interface
// documents its own concurrency expectations; for a Steerer in
// particular, sharing one queue across concurrent Runs splits
// inbound messages arbitrarily and is rarely what a caller wants.
type Runner struct {
	client   Client
	tools    ToolSource
	template templates.ChatTemplate

	// optional plumbing
	sink           EventSink
	convLock       *ConversationLock
	prompt         PromptSource
	steerer        Steerer
	truncator      Truncator
	compactor      compact.Compactor
	turnQuality    TurnQuality
	finalizeWarn   FinalizeWarn
	completionGate CompletionGate

	// loop configuration
	maxIterations   int // default for spec.MaxIterations == 0
	toolConcurrency int // max concurrent registry-tool calls per batch; <=1 = sequential
	// maxTokens caps each completion request's output tokens (max_tokens on
	// the wire). 0 = unset (provider/server default). A hard, deterministic
	// ceiling on a single generation — independent of model flags and of
	// timer scheduling under load, which the wall-clock iteration timeout is
	// not. Set via WithMaxTokens.
	maxTokens int
	// temperature sets the sampling temperature on each completion request.
	// 0 = unset (left off the request so the provider/server default applies).
	// Set via WithTemperature.
	temperature float32
	// thinkingBudgetBytes cuts an iteration that has emitted only reasoning
	// (thinking) tokens past this many bytes with no visible content or tool
	// call — the "stuck thinking" loop. 0 disables. Content-aware, so it
	// spares a healthy long generation that streams real output. Set via
	// WithThinkingBudget.
	thinkingBudgetBytes int

	// compaction configuration
	// keep sizes the compactor's keepRecent argument each iteration and
	// owns the token-pressure force-path. See the [keepPolicy] type for
	// the precedence of static / adaptive / pressure. Configure via
	// WithCompactKeepRecent / WithAdaptiveKeepRecent / WithTokenPressureCompact.
	keep keepPolicy

	// timeouts groups the three per-phase wall-clock guards (iteration
	// stream-drain, stream-idle, per-tool). See the [timeouts] type for
	// what each one bounds — notably, iteration does NOT cover tool
	// dispatch. Configure via WithIterationTimeout / WithStreamIdleTimeout
	// / WithToolTimeout.
	timeouts timeouts

	// emptyStreamBackoff is the base pause before the first retry of an
	// ErrEmptyStream iteration; it doubles per consecutive retry up to
	// emptyStreamRetryLimit. Defaults to defaultEmptyStreamBackoff;
	// WithEmptyStreamBackoff(0) makes retries immediate (used in tests).
	emptyStreamBackoff time.Duration

	// contextBreakdown gates the per-iteration per-role history tally
	// attached to IterationCompleted.Context. It's an O(history) walk +
	// allocation every iteration that only the TUI's context-window graph
	// reads, so it defaults off — a headless / eval run shouldn't pay for a
	// field nobody consumes. Enable via WithContextBreakdown.
	contextBreakdown bool

	// progress / observability
	// progressUpdater is called after each iteration's tool dispatch
	// completes with the running counters. Lets a consumer persist
	// intermediate state to durable storage so a SIGKILL on the
	// outer ctx leaves a recoverable progress trail rather than the
	// initial-insert "iter=0/tools=0" artifact. Nil disables.
	//
	// The callback reads TaskID from ctx (planted by taskscope.WithID at
	// the top of Run) — consumers don't need a per-task closure;
	// one updater can route to the right row by TaskID. Run synchronously
	// on the runner goroutine, so it should be fast (a single UPDATE
	// or a channel send), not block on network.
	progressUpdater ProgressUpdater
}

// New constructs a Runner. The only required argument is the LLM client
// (a streaming completion source — wrap an llm.Provider with
// ClientFromProvider when adapting). Everything else, including the tool
// source, is optional and supplied via options.
//
// The tool source defaults to an empty registry, so a Runner with no
// WithTools is a valid tool-less agent rather than a deferred nil-panic
// in the loop. Supply the live tool list with WithTools.
//
// A Runner defaults to [StderrSink] (tool-progress lines on stderr); pass
// [WithSink](nil) to silence it or [WithSink](mySink) for a custom observer.
func New(
	client Client,
	opts ...options.Option[Runner],
) *Runner {
	r := &Runner{
		client:        client,
		tools:         tools.NewRegistry(), // empty by default; install via WithTools
		template:      templates.Qwen3{},   // sane default — caller overrides via WithTemplate
		sink:          StderrSink,          // tool-progress to stderr by default; nil-out via WithSink(nil)
		truncator:     DefaultTruncator{},
		maxIterations: defaultMaxIterations,
		keep:          keepPolicy{static: 4}, // conservative default; override via WithCompactKeepRecent
		timeouts: timeouts{
			iteration:  defaultIterationTimeout,
			streamIdle: defaultStreamIdleTimeout,
			tool:       defaultToolTimeout,
		},
		emptyStreamBackoff: defaultEmptyStreamBackoff,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}
