# `zkit/agent/runner`

The canonical agent loop — `think → call tools → observe → repeat` —
as a transport-agnostic, drop-anywhere package. The same code drives
both a TUI (`zarlcode/tui`) and HTTP/SSE backends (`zarlai`).

## Six concerns, nothing else

The runner depends on six small consumer-implemented interfaces;
everything else is pushed onto the consumer side.

1. **LLM client** — [`Client`] (single method, streaming via `iter.Seq2[Chunk, error]`).
2. **The loop** — `Runner.Run(ctx, TaskSpec) (TaskResult, error)`.
3. **Dynamic tool list** — [`ToolSource`], re-snapshotted every iteration.
4. **Live-reloadable system prompt** — [`PromptSource`], called at the start of every Run.
5. **Event sink** — [`EventSink`] composite (5 sub-sinks), one method per event type.
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
- **System prompt**: `PromptSource.System(ctx, vars)` is called at the start of every Run. A source backed by a watched file or a database row picks up changes between turns automatically.
- **Steered messages**: `Steerer.Drain(ctx)` returns an `iter.Seq[llm.Message]` at the top of every iteration. An interactive harness (or the MCP notification bridge in `zkit/agent/mcp`) injects fresh user messages without restarting the loop.
- **Compaction**: `Compactor.Compact(ctx, messages, lastUsage)` is called at the start of every iteration after the first. The compactor decides whether the next request would overflow and returns a shrunken history.

No watchers, no broadcast machinery. The runner asks; the source
answers fresh.

## Key types

- [`Client`] — single-method LLM interface (`Complete` returning `iter.Seq2[Chunk, error]`). `ClientFromProvider` adapts a wider `llm.Provider`.
- [`ToolSource`] = [`Iterable`] + [`Executor`] — narrow read+dispatch contract the runner consumes.
- [`ToolRegistry`] extends `ToolSource` with `Register` / `Unregister` for producer-side mutation.
- [`EventSink`] — composite of [`ContentSink`], [`ToolSink`], [`ConversationSink`], [`SteerSink`], [`CompactionSink`]. [`NopSink`] provides no-op defaults so consumers can opt out of future events explicitly.
- [`PromptSource`] — single-method system-prompt source. [`PromptFunc`] and [`StaticPrompt`] are convenience adapters.
- [`Compactor`] — single-method history-shrinking policy. [`CompactFunc`] adapter.
- [`Truncator`] — tool-result trimming policy. [`DefaultTruncator`] (no spill) and [`SpillingTruncator`] (writes to disk) ship in the package.
- [`Steerer`] — single-method queued-message drain.
- [`ConversationLock`] — cooperative mutex with a sync.Cond inside; yields cleanly to a real-time conversation.

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
