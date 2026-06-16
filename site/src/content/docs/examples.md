---
title: Examples
description: Six runnable demonstrations, each isolating one pattern — most run deterministically with no LLM at all.
---

The [`examples/`](https://github.com/zarldev/zarlmono/tree/main/examples)
module contains six small, runnable harnesses. Each isolates one
pattern; each has its own README; most accept `-scripted` and run a
deterministic fake client so you can watch the machinery without an
API key.

| Example | What it demonstrates |
|---|---|
| `healthcheck` | A world-verifying goal: the agent probes a fake server farm until every endpoint reports healthy. Schema + fan-out guardrails policing the calls. |
| `releasegate` | Pre/post guardrails around a workflow: the agent may only publish after every required check is green, and the goal confirms the publish actually happened. |
| `hnupvote` | Browser automation under pursue: a real chromedp session where the oracle is verified world state and a login wall forces the re-drive path. |
| `spawn_worker` | Hierarchical decomposition: a coordinator delegates to researcher / reviewer / coder sub-agents with per-mode tool gating and a depth cap. |
| `stuck_recovery` | The decompose guardrail's graduated response — pass, advisory, fatal — as an agent repeats a failing search, then recovers by delegating. |
| `long_conversation` | Compactor integration: a pressure-gated compactor keeps a long exploration inside the context window without orphaning tool calls. |

## Running them

```sh
# deterministic, no LLM
go run ./examples/healthcheck -scripted

# real provider — flags are uniform across examples
go run ./examples/healthcheck -provider anthropic -model claude-sonnet-4-6
go run ./examples/releasegate -provider llamacpp
```

Provider selection goes through the same
[backends registry](/zarlmono/providers/#the-backends-registry) as
everything else: `-provider` names a builtin, keys come from the
standard env vars, `-base-url` (or `LLAMACPP_BASE_URL` etc.)
overrides endpoints.

## Reading order

Start with `healthcheck` — it's the smallest complete loop with a
real goal. Then `releasegate` for guardrails as policy, and
`stuck_recovery` for what graduated advisories look like in
practice. `hnupvote` is the one to read when you want to see pursue
driving something genuinely flaky (a browser) to a verified outcome.

Each example's README walks through its harness construction;
`examples/patterns.md` in the repo collects the recurring shapes —
scripted clients, fake filesystems, oracle design — you'll reuse in
your own harnesses.
