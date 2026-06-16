---
title: Sub-agents
description: The spawn_agent tool — single-hop delegation with depth and fan-out caps, because unbounded recursion is how context windows die.
---

`zkit/agent/tools/spawn` provides `spawn_agent`: a registry-compatible
tool that runs a focused sub-task in a fresh `runner.Run` and returns
only the child's summary as the tool result. The parent's context
stays clean; the child starts from nothing but the prompt it was
given.

## Wiring

```go
import "github.com/zarldev/zarlmono/zkit/agent/tools/spawn"

r := runner.New(client, runner.WithTools(reg) /* … */)
reg.Register(spawn.New(r))                          // default depth cap: 1
reg.Register(spawn.New(r, spawn.WithMaxDepth(2)))   // if you really mean it
```

Register spawn *after* constructing the runner — the tool captures
the parent runner, and since the registry is re-snapshotted every
iteration, post-construction registration is callable on the next
turn. It's a separate package on purpose: consumers that don't want
sub-agent recursion simply don't import it.

## The two caps

**Depth, default 1.** The runner plants the current depth on ctx;
the spawn tool reads it back and refuses past the cap. Depth 1 means
a parent can delegate, but the child cannot spawn grandchildren.
Left uncapped, capable models treat spawn as a free fan-out
primitive and build trees of children whose results never converge —
burning the context window assembling an org chart instead of doing
the work. `WithMaxDepth(0)` disables spawning entirely.

**Fan-out, via the [fan-out guardrail](/zarlmono/guardrails/#fan-out--exploration-budgets).**
Even at depth 1, one iteration can emit many spawn calls. A per-task
budget (zarlcode uses 3) turns spawn into what it should be: a small
handful of parallel, single-hop delegations.

The combination is the design: depth stops recursion, fan-out stops
explosion.

## Named sub-agents

`spawn.WithAgentResolver(fn)` routes an `agent="reviewer"` argument
to a different runner — different prompt, different model, different
tool gates. A parent on a cheap local model can delegate review to a
stronger hosted one. Resolution failure falls back to the parent
runner with a notice in the result, rather than failing the call.

## Work modes

The `mode` argument gates the child's tool surface:

- **`explore`** — read-only investigation. The host blocks file
  edits and shell execution. Safe for mapping codebases and
  answering "what does X do" questions.
- **`verify`** — build and test only. No file edits, but shell
  (for `go test`, `go build`) is allowed.
- **`implement`** — full tool surface. The default.

Mode enforcement happens at the tool level — an explore sub-agent
literally cannot call `write` or `edit`, regardless of what the
model attempts.

## Parallel dispatch

When the parent emits multiple `spawn_agent` calls in a single
response, the runner dispatches them concurrently. A small handful
(researcher + reviewer + coder) is the intended shape; the fan-out
guardrail caps the spawn budget per task to prevent explosion.

## Spawn planner

The `agent` argument names a target sub-agent profile. When the
model gets the name wrong (typo, hallucinated name), the
`SpawnPlanner` — a grammar-constrained recovery step — maps it
to the nearest registered agent name rather than failing the call.
Resolution failure ultimately falls back to the parent runner with
a notice in the result.
## What the child sees

- the prompt the parent wrote — no inherited history
- the same tool registry (minus what your gates exclude)
- its own iteration budget

What comes back is the child's final summary as a single tool
result. Sub-agents are context isolation: the parent pays a sentence
for work that cost the child fifty tool calls — that's the deal, and
it's a good one as long as someone (the runner's depth tracking, the
guardrail's budget) is watching the bill.
