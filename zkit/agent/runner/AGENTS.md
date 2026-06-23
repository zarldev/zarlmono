# AGENTS.md — `zkit/agent/runner`

Notes for editors working in this package. The README documents *what's* here; this file documents *why* it's shaped this way and *how* to extend it without regressing the design.

## Why this package exists at all

The runner is a transport-agnostic agent loop — the canonical "think → call tools → observe → repeat" — that powers both the TUI (`zarlcode/tui`) and HTTP/SSE backends (`zarlai`). The bar for a shared package is real reuse of the same code in multiple consumers, and the absence of TUI- or HTTP-shaped concepts in this package's contract is the design constraint that makes sharing possible.

## The interface rule

Every concern the runner has lives behind a small interface. When adding a feature, ask first: *can this be a method on an existing interface, a new dedicated interface, or — best — pushed onto the consumer side entirely?*

- `Client` — LLM streaming. One method.
- `ToolSource` — reads + executes tools. `Iterable` + `Executor`.
- `PromptSource` — system prompt. One method.
- `EventSink` — runner observability. Composite of six typed sub-sinks.
- `Steerer` — user messages between iterations. One method.
- `compact.Compactor` — history compaction. One method.

Optional: `ConversationLock`, `TurnQuality`, `FinalizeWarn`, tool gating, `Truncator`. Each one or two methods.

If you find yourself adding a multi-method behemoth, you're probably trying to do too much in the runner. Reconsider whether the work belongs on the consumer side.

## Why pull-based, not push

The runner asks its sources for fresh data on every iteration / every Run. No observer pattern, no file-watcher, no cache-invalidation broadcast. This is on purpose:

- Sources can be backed by anything (file, DB, HTTP) without changing the runner contract.
- Live reload is automatic — re-read on every call, no glue code.
- Test fakes are trivial: a fake `PromptSource` returning a constant; a fake `ToolSource` wrapping a fixed slice.

The cost is one extra read per iteration — a few hundred microseconds for tools and prompts, not a hot path.

## Why compile-time exhaustiveness on EventSink

`EventSink` is a composite of six small sinks (`ContentSink`, `ThinkingSink`, `ToolSink`, `ConversationSink`, `SteerSink`, `CompactionSink`). Adding a method to any of them breaks every full-`EventSink` implementer until they handle it. That's intentional: a UI rendering sub-agent indentation needs to know when a new event lands, or it silently drops it.

Consumers that want to ignore future events embed `NopSink`:

```go
type myMetricsSink struct{ runner.NopSink }
func (s *myMetricsSink) OnToolCompleted(e runner.ToolCompleted) { /* count */ }
```

The opt-out is explicit. Don't add `// no-op` overrides for events you should be handling.

To add a new event type: add a flat payload struct (with `TaskID` and `Depth`) to `events.go`; add the method to one sub-sink (or define a new sub-sink and add it to `EventSink`); add a `NopSink` no-op override; add a publish helper; wire it into the loop in `run.go`. Existing implementers fail to compile until they handle it — the whole point.

## Why `ClientFromProvider` exists

The wider `llm.Provider` interface has many consumers across the monorepo. The runner's `Client` is a *narrower* view — single method, streaming-only — so `Provider` implementations don't need to change. `ClientFromProvider` adapts an `llm.Provider` to satisfy `Client`. The runner depends on `Client`, not `llm.Provider`. Don't grow `Client`; new LLM methods go elsewhere.

## Why Compactor is a runner concern

Compaction has two triggers:

1. **Reactive** — a call fails with context overflow; compact and retry. This lives on the consumer (catch the error after `Run` returns, compact `result.Messages`, re-Run with the compacted context).
2. **Proactive** — usage shows we're approaching the window; compact *before* the next call. This only the runner sees between iterations, so the runner consults `compact.Compactor` there.

The runner skips iteration 0 (no prior usage to drive a decision) and calls `Compact` at the top of every iteration after. Implementations MUST preserve the leading system message at index 0 if present — the loop relies on it.

## Prompt text is a control plane

The runner deliberately treats prompts as a portable, inspectable control plane, not as learned policy. `PromptSource` returns ordinary text because zkit must work across OpenAI-compatible endpoints, Anthropic, Google, local llama.cpp/Ollama servers, and OAuth-backed coding providers without assuming gradient access, fine-tuning, or model-family-specific soft prompts.

That trade-off makes prompt influence easy to inspect and hot-reload, but it also means prompt prose is behaviour-bearing source material. Keep prompt fragments small, attributable, reviewed, and testable. Prefer putting capability in tools and narrow interfaces; use prompt text for operating contracts and task-specific guidance that needs to be visible to operators.

## Why context.AfterFunc instead of polling

`ConversationLock.Wait` uses `sync.Cond` (Release wakes waiters immediately) plus `context.AfterFunc` (ctx cancellation broadcasts the cond). No poll loop, no timer drift. Don't add poll loops in this package: either a sync primitive wakes you (Cond, channel, AfterFunc) or the design is wrong.

## Integrating from a new consumer

Three reference shapes, by complexity:

**Headless / background.** No sink, steerer, or prompt source — just a Client, a ToolSource, and a `TaskSpec`:

```go
r := runner.New(client, runner.WithTools(tools))
result, _ := r.Run(ctx, runner.TaskSpec{Prompt: "..."})
```

**Interactive TUI** (`zarlcode/tui`): a sink translates runner events into UI messages; a steerer accepts queued user lines; the prompt source re-renders from skills + workspace each turn. See `zarlcode/tui/live.go` and `zarlcode/tui/teasink/`.

**HTTP/SSE backend** (zarlai-shaped): a sink translates runner events into SSE writes; one runner per request, or a long-lived runner with per-request `TaskSpec`s. Construction state is read-only after `New`, so concurrent Run calls don't corrupt each other — but watch shared plumbing (below).

## Concurrency under concurrent Runs

Read-only after construction (safe to share): client, tools, prompt, template, max iterations. Shared mutable plumbing needs care:

- **EventSink** — receives interleaved events from multiple Runs; needs internal locking. The shipped `runnertest.Sink` uses a mutex.
- **Steerer** — one shared queue splits inbound messages across Runs arbitrarily; use one per Run.
- **Truncator** — invoked from goroutines under `WithToolConcurrency`; the shipped truncators are concurrency-safe.
- **Compactor** — concurrent Runs invoke concurrently; implementations should be parallel-safe.
- **MemoSource** (when wired as the ToolSource) — safe to share, but the Get→Execute→Set sequence is NOT atomic: two concurrent identical calls in one task can both miss and both re-run the inner tool. Fine for pure tools.

## Recovery mechanisms

Three independent budgets, each resetting on any successful iteration:

- **Tool-call JSON repair** (limit 3). On malformed tool-call JSON from the provider, `llm/repair` extracts a best-effort call before the runner marks a hard failure. After three failed repairs in a row, the error goes terminal.
- **Empty-stream retry** (limit 3). When the provider opens a stream but delivers nothing (a gateway timeout on heavy prefill is the canonical case), the runner backs off and retries; three retries cover transient cuts without spinning against a down backend.
- **Turn-quality correction** (bounded by the hook). The optional `TurnQuality` hook injects synthetic user messages when a turn is degenerate (e.g. thinking consumed all tokens), with its own per-run limit.

## Optional hooks

- **TurnQuality** — inspect zero-tool-call turns and decide whether to inject a correction or let the loop exit. The default detector catches thinking-budget exhaustion on small models.
- **FinalizeWarn** — with N iterations left, inject a one-shot "wrap up" message so the loop doesn't truncate silently at the cap.
- **Tool gating** — block specific tools in a given context; gated tools are reported to the sink but not dispatched.
- **ConversationLock** — yield the loop while a real-time conversation is active, to share a limited LLM.
- **Truncator** — cap oversized tool results (tail-keep, or spill to disk for recovery). Defaults to trim-only.
- **Steerer** — inject queued user messages between iterations.

## Things to never do

- **Don't add ctx-planted state.** `keyDepth` and `keyTaskID` are the only context values the runner plants — for spawn-agent recursion tracking and task-scoped caching. Adding more reintroduces the implicit, hard-to-trace lookups this package deliberately avoids. New state goes on `TaskSpec` (per-task), `Runner` fields (per-runner), or closure captures (per-tool).
- **Don't grow `TaskSpec`.** It's the input to `Run`; every field is a public commitment. Tool-specific data goes on the tool instance.
- **Don't import outside the allow-list:** `zkit/agent/compact`, `zkit/agent/taskscope`, `zkit/ai/llm`, `zkit/ai/llm/repair`, `zkit/ai/llm/templates`, `zkit/ai/tools`, `zkit/cache`, `zkit/options`, `golang.org/x/sync/errgroup`, and the standard library. The runner's import graph is its most public commitment, and new consumers depend on this list staying minimal.
- **Don't reach into `*Runner` from outside the package.** Tools and guardrails that need task-scoped buckets use `taskscope.IDFrom` / `taskscope.DepthFrom` directly.
- **Don't return errors consumers can't switch on.** Sentinels (`ErrCancelled`, `ErrCompact`, `ErrEmptyStream`, …) wrap real failures; consumers `errors.Is` against them. A new terminal failure path needs a sentinel.

## Testing patterns

- **Use `runnertest`** for shared fakes: scriptable Client, recording Sink (embeds `NopSink`), minimal Tool, chunk constructors.
- **Use `*tools.Registry`** as the ToolSource — it already satisfies the interface.
- **Use `runner.StaticPrompt(body)`** for prompt sources.
- **Use `testing/synctest`** for tests that wait on a goroutine or coordinate concurrent state — `synctest.Wait()` blocks until every goroutine in the bubble is blocked, the right primitive instead of `time.Sleep`.
- **Race-test concurrency paths.** Parallel tool dispatch is exercised under `-race`; new concurrent paths add similar coverage.
