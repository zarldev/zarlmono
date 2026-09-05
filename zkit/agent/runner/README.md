# `zkit/agent/runner`

The canonical agent loop — `think → call tools → observe → repeat` —
as a transport-agnostic, drop-anywhere package. The same code drives
the zarlcode TUI (`zarlcode/tui`).

## Six concerns, nothing else

The runner depends on six small consumer-implemented interfaces;
everything else is pushed onto the consumer side.

1. **LLM client** — [`Client`] (single method returning lazy, synchronous `llm.CompletionStream`).
2. **The loop** — `Runner.Run(ctx, TaskSpec) (TaskResult, error)`.
3. **Dynamic tool list** — [`ToolSource`], re-snapshotted every iteration.
4. **Live-reloadable system prompt** — [`PromptSource`], called when each Run assembles its initial history.
5. **Event sink** — [`EventSink`] composite (8 focused sub-sinks), one method per event type.
6. **Compaction policy** — [`Compactor`], called between iterations to shrink history.

Optional plumbing: [`Steerer`] (queued user messages), [`ConversationLock`]
(yield to a real-time conversation), [`Truncator`] (cap oversized tool
results).

## Quick start

```go
client := runner.ClientFromProvider(myLLMProvider)   // wraps an llm.Provider
toolReg := tools.NewRegistry()
toolReg.Register(myTool)

r := runner.New(client,
    runner.WithTools(toolReg),
    runner.WithSink(myEventSink),
    runner.WithPrompt(runner.StaticPrompt("You are a helpful assistant.")),
    runner.WithMaxIterations(20),
)

result, err := r.Run(ctx, runner.TaskSpec{
    Prompt: "summarise today's news",
})
```

A Runner with no sink, no prompt source, and no compactor still runs
— the loop just emits no events, sends no system message, and never
shrinks history. Useful for headless background tasks.

## Live reload

Every state a consumer wants to mutate at runtime flows through a
**pull-shaped** boundary:

- **Tools**: `ToolSource.Tools()` returns `iter.Seq[tools.Tool]` — the runner re-reads every iteration. Register a tool mid-run and it's callable on the next turn.
- **System prompt**: `PromptSource.System(ctx, vars)` is called when each `Run` assembles its initial history. A source backed by a watched file or database row picks up changes between runs.
- **Steered messages**: `Steerer.Drain(ctx)` returns an `iter.Seq[llm.Message]` at the top of every iteration. An interactive harness (or the MCP notification bridge in `zkit/agent/mcp`) injects fresh user messages without restarting the loop.
- **Compaction**: `Compactor.Compact(ctx, messages, keepRecent)` is called at the start of every iteration after the first. The compactor decides whether the next request would overflow and returns a shrunken history.

## Provider-neutral history and replay

Prompt behavior is shared runner policy: the runner sends the same inspectable
system/task/compaction prompts through the narrow `Client` contract rather than
embedding behavior in provider adapters. Assistant history has two distinct
reasoning channels:

- `ReasoningContent` is the displayable, provider-neutral projection accumulated
  from streamed thinking. Sinks may render it and adapters may reshape it for
  their normal history format.
- `ContinuationItems` contains complete opaque provider-native output items
  needed for lossless continuation. The runner clones completed items into the
  assistant turn in native output order; it does not parse them.

An adapter replays only continuation items matching its own provider and format,
byte-for-byte and at their recorded output positions. It ignores foreign items,
which makes reusing history after a provider switch safe. Compaction counts
opaque payload bytes and deep-copies retained turns, but never summarizes or
partially truncates an item; collapsing or dropping its owning old message is
the compaction boundary.

The loop remains stateless across requests. Provider beta controls and server-side
conversation chaining (for example previous-response IDs) are intentionally
deferred until they justify stable typed, opt-in interfaces. They are not inferred
from opaque replay items and do not widen `llm.Provider` or `Client`.

No watchers, no broadcast machinery. The runner asks; the source
answers fresh.

## Key types

- [`Client`] — single-method LLM interface (`Complete` returning `llm.CompletionStream`). `ClientFromProvider` explicitly narrows an `llm.Provider`. Calling `Complete` only constructs the stream; work and operational errors begin during synchronous iteration.
- [`ToolSource`] = [`Iterable`] + [`Executor`] — narrow read+dispatch contract the runner consumes.
- [`ToolRegistry`] extends `ToolSource` with `Register` / `Unregister` for producer-side mutation.
- [`EventSink`] — composite of [`ContentSink`], [`ThinkingSink`], [`ToolSink`], [`WorkspaceWaitSink`], [`ConversationSink`], [`SteerSink`], [`CompactionSink`], and [`DiagnosticSink`]. [`NopSink`] provides no-op defaults so consumers can opt out of future events explicitly.
- [`PromptSource`] — single-method system-prompt source. [`PromptFunc`] and [`StaticPrompt`] are convenience adapters.
- [`Compactor`] — single-method history-shrinking policy. [`CompactFunc`] adapter.
- [`Truncator`] — tool-result trimming policy. [`DefaultTruncator`] (no spill) and [`SpillingTruncator`] (writes to disk) ship in the package.
- [`Steerer`] — single-method queued-message drain.
- [`ConversationLock`] — cooperative mutex with a sync.Cond inside; yields cleanly to a real-time conversation.

## Completion streaming

`Client.Complete` returns a fully lazy, one-shot stream with no outer error. The runner ranges it synchronously: successful EOF completes the invocation, while a provider failure is one terminal zero-chunk error yield. Finish/usage observations are metadata rather than completion sentinels. Request state is borrowed through iteration and yielded reference-backed chunk state only through the callback, so retaining consumers clone synchronously. Cancellation must propagate into provider setup and reads; no chunks channel or producer goroutine sits between provider and runner.

## Sentinel errors

Consumers `errors.Is` against `TaskResult.Err` (or the error returned
from `Run`) instead of parsing strings:

- `ErrInvalidIterations` — `TaskSpec.MaxIterations` was negative.
- `ErrCancelled` — the run was cancelled mid-loop (wraps `ctx.Err()`).
- `ErrPromptRender` — the `PromptSource` returned an error.
- `ErrCompact` — the `Compactor` returned an error.

## Testing

Use `zkit/agent/runner/runnertest` for shared fakes — a scriptable
`Client`, recording `Sink`, minimal `Tool`, and chunk constructors —
so test files don't reinvent them.

## Where to look next

- [`AGENTS.md`](AGENTS.md) — design rationale, integration patterns, and what *not* to do.
- [`zarlcode/tui/`](../../../zarlcode/tui/) — the canonical consumer; see `shell.go:rebuildRunner` for full wiring.

[`Client`]: https://pkg.go.dev/github.com/zarldev/zarlmono/zkit/agent/runner#Client
[`ToolSource`]: https://pkg.go.dev/github.com/zarldev/zarlmono/zkit/agent/runner#ToolSource
[`Iterable`]: https://pkg.go.dev/github.com/zarldev/zarlmono/zkit/agent/runner#Iterable
[`Executor`]: https://pkg.go.dev/github.com/zarldev/zarlmono/zkit/agent/runner#Executor
[`ToolRegistry`]: https://pkg.go.dev/github.com/zarldev/zarlmono/zkit/agent/runner#ToolRegistry
[`EventSink`]: https://pkg.go.dev/github.com/zarldev/zarlmono/zkit/agent/runner#EventSink
[`ContentSink`]: https://pkg.go.dev/github.com/zarldev/zarlmono/zkit/agent/runner#ContentSink
[`ToolSink`]: https://pkg.go.dev/github.com/zarldev/zarlmono/zkit/agent/runner#ToolSink
[`ConversationSink`]: https://pkg.go.dev/github.com/zarldev/zarlmono/zkit/agent/runner#ConversationSink
[`SteerSink`]: https://pkg.go.dev/github.com/zarldev/zarlmono/zkit/agent/runner#SteerSink
[`CompactionSink`]: https://pkg.go.dev/github.com/zarldev/zarlmono/zkit/agent/runner#CompactionSink
[`NopSink`]: https://pkg.go.dev/github.com/zarldev/zarlmono/zkit/agent/runner#NopSink
[`PromptSource`]: https://pkg.go.dev/github.com/zarldev/zarlmono/zkit/agent/runner#PromptSource
[`PromptFunc`]: https://pkg.go.dev/github.com/zarldev/zarlmono/zkit/agent/runner#PromptFunc
[`StaticPrompt`]: https://pkg.go.dev/github.com/zarldev/zarlmono/zkit/agent/runner#StaticPrompt
[`Compactor`]: https://pkg.go.dev/github.com/zarldev/zarlmono/zkit/agent/runner#Compactor
[`CompactFunc`]: https://pkg.go.dev/github.com/zarldev/zarlmono/zkit/agent/runner#CompactFunc
[`Truncator`]: https://pkg.go.dev/github.com/zarldev/zarlmono/zkit/agent/runner#Truncator
[`DefaultTruncator`]: https://pkg.go.dev/github.com/zarldev/zarlmono/zkit/agent/runner#DefaultTruncator
[`SpillingTruncator`]: https://pkg.go.dev/github.com/zarldev/zarlmono/zkit/agent/runner#SpillingTruncator
[`Steerer`]: https://pkg.go.dev/github.com/zarldev/zarlmono/zkit/agent/runner#Steerer
[`ConversationLock`]: https://pkg.go.dev/github.com/zarldev/zarlmono/zkit/agent/runner#ConversationLock
