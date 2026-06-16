---
title: Compaction
description: Histories grow, context windows don't. Four engines behind one two-method interface, from byte-level trimming to LLM-written briefings.
---

The runner consults a `compact.Compactor` between iterations to
shrink older history without breaking the `tool_call_id` linkage the
provider APIs require — every tool call must keep a matching result
message, or the next request 400s.

## The interface

```go
type Compactor interface {
	Compact(ctx context.Context, history []llm.Message, keepRecent int) (Result, error)
}
```

`keepRecent` is the runner's promise: preserve at least the last N
messages verbatim. Everything older is the engine's to shrink.

The optional `Prober` interface is the cheap pre-check:

```go
type Prober interface {
	WouldReduceBytes(history []llm.Message, keepRecent int) int
}
```

The runner asks before compacting; `<= 0` skips the call entirely.
This matters more than it looks — an always-trim engine without the
gate fires dozens of no-op compactions per long task.

## The engines

### Structural — always-trim, no LLM

Byte-level rules over everything older than the keep window: user
messages never touched (load-bearing intent), long assistant
narrative truncated, long tool results replaced with an elision
marker ("re-run to recover"), tool-call linkage always preserved.
Milliseconds per call. The blunt instrument.

### Tiered — progressive, no LLM

```go
runner.WithCompactor(compact.NewTiered(ctxWindowTokens))
```

Three phases keyed to history size against a byte budget: at 60%
tool-result bodies truncate, at 75% assistant narrative truncates
too, at 90% tool results become one-line placeholders. **Below 60%
it's a pure no-op with zero allocation.** Reasoning is preserved
longest — it's the model's interpretive context for the next turn.
The default choice for long sessions where pressure is occasional.

### Adaptive — token-budget-driven

`compact.NewAdaptive(provider, model)` varies the keep count based
on remaining token budget: tighter when the window is nearly full,
looser when there's headroom. The adaptive strategy can also act as
the `Prober` — it knows the budget and decides when compaction is
worth the cost.

### Pressure — fraction-based trigger

`WithTokenPressureCompact` adds a budget-fraction trigger: when
estimated tokens exceed the configured fraction of the context
window, compaction fires automatically. Pair it with any engine —
the trigger is independent of the strategy.
### Summary — LLM-written narrative

Sends everything older than the keep window to a (typically smaller,
cheaper) model and replaces it with a single summary message. Tool
result bodies are paraphrased away — the model can re-fetch exact
bytes via tools if it needs them.

One subtlety the implementation handles for you: the cut point snaps
backward past any tool-result messages so the assistant turn that
owns them rides into the kept window. Splitting between a tool call
and its result orphans a `tool_call_id`, and the next request fails.

### Executive — Summary plus structured state

```go
compact.NewExecutive(provider, model, stateProvider)
```

Same LLM-driven shape, plus structured sections built from a
consumer-supplied `StateProvider`: plan progress, working files,
tool-usage counts, then the narrative. For coding agents this is the
right briefing format — the structured state *is* what a continuing
agent needs to keep momentum. The host application implements
`StateProvider`; plan progress typically comes straight from
`update_plan` calls.

## Adaptive keep windows

A static "keep the last 4 messages" breaks in both directions: one
huge tool result dominates the window, or a run of short turns
starves the model of recent memory.

```go
runner.WithAdaptiveKeepRecent(targetTokens, minKeep, maxKeep)
```

walks the tail of history accumulating estimated tokens and sizes
the keep window to fit the target, clamped to the bounds. For a
32k-window model, `(8000, 2, 12)` is a sensible starting point.

## Choosing

| You want | Use |
|---|---|
| cheap, predictable, always works | Structural |
| do nothing until there's actual pressure | Tiered |
| compressed narrative, exact bytes expendable | Summary |
| a continuing coding agent's briefing | Executive |
| token-budget-aware, only when worth it | Adaptive |
| automatic trigger at a pressure threshold | Tiered + Pressure |
All four implement the same interface; the runner takes whichever
you hand it via `WithCompactor`, and swapping engines is a one-line
change.
