---
title: Runner
description: One method is the whole agent loop — and the eight things that make it survive real models.
---

`zkit/agent/runner` implements a single method:

```go
func (r *Runner) Run(ctx context.Context, spec TaskSpec) (TaskResult, error)
```

and that method is the entire think → call tools → observe → repeat
loop. The shape, per iteration:

```
wait on conversation lock          (yield to a real-time conversation)
drain steerer queue                (inject user messages queued mid-task)
maybe inject finalize-warn         (when iter is near the cap)
maybe compact                      (gated by a cheap Prober pre-check)
shape messages via chat template   (Qwen3 / Gemma4 / passthrough)
stream Provider.Complete           (watchdogs on iteration + stream-idle)
append assistant message
if tool calls:  dispatch each through the registry, append results
else:           terminal — exit with TerminalCompleted
progress callback                  (persist per-iter state)
```

## Construction

```go
r := runner.New(client,
	runner.WithTools(source),                    // ToolSource, re-read every iter
	runner.WithPromptText("You are…"),           // or WithPrompt(PromptSource)
	runner.WithMaxIterations(20),
	runner.WithToolTimeout(45*time.Second),
	runner.WithIterationTimeout(5*time.Minute),
	runner.WithStreamIdleTimeout(90*time.Second),
	runner.WithCompactor(compact.NewTiered(32_000)),
	runner.WithSink(mySink),                     // typed event stream
)
res, err := r.Run(ctx, runner.TaskSpec{Prompt: "fix the failing test"})
```

`client` is a `runner.Client` — a one-method narrowing of
`llm.Provider` produced by `runner.ClientFromProvider`. It stays one
method on purpose; if you need more from the LLM, that belongs
elsewhere.

A runner with no sink, no prompt source, and no compactor still runs.
The loop just emits no events, sends no system message, and never
shrinks history — useful for headless background tasks and tests.

## Results

`Run` returns a `TaskResult` whose `Reason` says why the loop ended:

| Reason | Meaning |
|---|---|
| `TerminalCompleted` | the model stopped calling tools — its idea of done |
| `TerminalMaxIterations` | budget exhausted |
| `TerminalCancelled` | ctx cancelled (user stop, early-stop watcher) |
| `TerminalError` | unrecoverable provider or compaction error |

A terminal reason is a fact about the *run*, not the *task*. Whether
the work is actually done is [pursue's](/zarlmono/pursue/) job.

## What makes it survive real models

A textbook ReAct loop dies in production within the hour. The
differences that matter:

**Pull-based sources.** Prompt, tools, steered messages, and
compaction policy are re-read every iteration. Live reload is
automatic — there is no cache to invalidate.

**A watchdog hierarchy.** The iteration timeout caps total wall time
per turn; the stream-idle timeout caps the silence between chunks.
Each fires a distinct sentinel error, so "we cut it off" is
distinguishable from "the connection died" and from "the user hit
ctrl+c". Small local models love to think forever without emitting —
without the watchdogs that looks identical to a hang.

**Direct synchronous stream consumption.** `Complete` constructs a fully lazy `llm.CompletionStream`; provider setup and I/O start only when the runner invokes it. The runner processes each yield synchronously and returns the downstream boolean directly, preserving backpressure and borrowed chunk lifetimes. There is no forwarding channel or producer goroutine. Provider cancellation is part of the contract, while the iteration/idle wrapper owns and joins its watchdog and preserves the context cause; time spent in the runner's yield callback does not count as provider idle. Successful EOF completes the stream, and a terminal failure is one zero-chunk error yield—never a `Done` chunk or outer setup error.

**Soft recovery for malformed tool JSON.** llama.cpp validates
tool-call JSON server-side and 500s before the runner ever sees the
arguments. The runner pattern-matches that failure, injects a
corrective user message ("your tool-call JSON was malformed —
re-emit with proper escaping"), and continues — capped at three
consecutive attempts so a permanently confused model can't loop
forever.

**The finalize warning.** Near the iteration cap the runner injects
a one-shot "you have N iterations left — wrap up" message. Models
otherwise explore right through iteration 19 of 20 and run out of
budget with nothing to show.

**Steering and the conversation lock.** `WithSteerer` lets a UI queue
user messages mid-task; the runner drains the queue at the top of
each iteration. `WithConversationLock` makes a long-running
background task yield while a real-time conversation is happening,
then resume.

**Per-iteration progress persistence.** `WithProgressUpdater` fires
after every dispatch with `(iter, totalToolCalls)`. If the process
gets SIGKILLed at iteration 46, the record says iteration 46 — not
the zero state from the initial insert.

**Typed, exhaustive events.** `WithSink(EventSink)` streams the run —
content deltas, tool start/finish, compaction, steering. See
[Observing a run](#observing-a-run) below.

## Observing a run

`WithSink` is how you watch a run happen — render a TUI, log
telemetry, drive a progress bar. `EventSink` is six small sub-sinks
composed together:

| Sub-sink | Fires on |
|---|---|
| `ContentSink` | each streamed assistant-text delta |
| `ThinkingSink` | each streamed reasoning delta |
| `ToolSink` | tool started / completed / failed |
| `ConversationSink` | run started / ended, and each iteration |
| `SteerSink` | queued user messages injected mid-run |
| `CompactionSink` | history compaction applied |

You rarely want all ten methods. **Embed `runner.NopSink` and
override only the events you care about** — it satisfies the whole
interface with no-ops, so this is a complete, valid sink:

```go
// Print a line whenever a tool runs; ignore everything else.
type toolLogger struct{ runner.NopSink }

func (toolLogger) OnToolStarted(e runner.ToolStarted) {
	fmt.Printf("→ %s %v\n", e.ToolName, e.Parameters)
}
func (toolLogger) OnToolCompleted(e runner.ToolCompleted) {
	fmt.Printf("✓ %s\n", e.FormattedResult)
}

r := runner.New(client,
	runner.WithTools(reg),
	runner.WithSink(toolLogger{}),
)
```

`runner.NopSink{}` on its own is the do-nothing sink — the default
when you pass no `WithSink` at all, and useful as an explicit
"discard all events" in tests.

Embedding is what makes the exhaustiveness contract bearable: a *full*
`EventSink` implementation (no embed) fails to compile the day a new
event method is added, which is the point — a real UI shouldn't
silently drop a new event kind. Consumers that genuinely want to
ignore the future opt out by embedding `NopSink`, and say so by doing
it.

:::caution
Sink methods must be safe for concurrent calls — `WithToolConcurrency(N>1)`
fires tool events from parallel goroutines, and one runner can service
concurrent `Run`s. A mutex-guarded append or a channel send is enough;
or wrap any sink in `runner.NewSyncSink(s)` to serialise every call
behind one mutex.
:::

## Prompt sources

`WithPromptText` is the 90% case. For live-editable system prompts,
implement `PromptSource` — it's consulted at the start of every Run,
and `runner.StaticPrompt(body)` adapts a plain string. `TaskSpec.PromptVars`
threads per-task variables (persona prefix, user name, whatever your
template needs) into the source.

## Compaction hooks

The runner consults its `Compactor` between iterations, gated by the
`Prober` pre-check so no-op compactions cost nothing. Keep-window
sizing is either static (`WithCompactKeepRecent`) or adaptive
(`WithAdaptiveKeepRecent(targetTokens, minKeep, maxKeep)`), and
`WithTokenPressureCompact` adds a budget-fraction trigger. The
engines themselves are covered in [Compaction](/zarlmono/compaction/).

## More options

The options above are the common ones. A handful of additional knobs
cover specific failure modes observed in long-running sessions:

**`MemoSource`.** Wraps a tool source and memoises tools that declare
themselves pure (`read`, `ls`, `grep`, `glob`). A re-read of the same
path returns cached bytes without touching the tool — or the guardrail
chain — and stops repeated identical reads from eating a fan-out
budget. Entries drop per task on completion.

**`ThinkingBudget`.** Caps the total bytes of reasoning a model can
emit without producing content. Local models sometimes think forever
without answering — this cuts the loop when reasoning alone fills the
budget, before the iteration timeout fires.

**`EmptyStreamBackoff`.** When a provider returns zero chunks (not
uncommon on overloaded local endpoints), the runner retries with
exponential backoff rather than surfacing an opaque error.

**`Truncator`.** Controls how tool results are trimmed before they
enter the conversation. The default keeps up to 2000 lines within a
200KB byte budget and spills the full output to disk when either limit is
exceeded — the model sees a head/tail excerpt with a path to the full file.

**`ToolGate`.** Run-scoped capability filtering. A `researcher` profile might
hide every workspace-mutating `ToolSpec`; a deeper child might hide `agent_spawn`.
`WithToolGate(ctx, fn)` stores a `func(tools.ToolSpec) bool` on the run context.
Rejected tools are omitted from the model-visible surface and refused if called.

**`TurnQuality`** and **`CompletionGate`.** Both are consulted when an iteration
has no tool calls and would otherwise complete. `TurnQuality` can reject a degenerate
assistant turn and inject a corrective user message. `CompletionGate` independently
checks whether required durable work happened before allowing the terminal exit.
They are consumer extension points, not pre-dispatch hooks.

**`runnertest`.** A deterministic fake client for testing agent
wiring. `runnertest.NewClient` replays a scripted sequence of turns
— no API key, no network, no flakes. Every example in the repo
leans on this to run end-to-end with `-scripted` and no LLM at all.