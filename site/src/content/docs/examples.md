---
title: Examples
description: Runnable demonstrations, each isolating one pattern — most run deterministically with no LLM at all.
---

The [`examples/`](https://github.com/zarldev/zarlmono/tree/main/examples)
module contains small, runnable harnesses. Each isolates one
pattern; each has its own README; most accept `-scripted` and run a
deterministic fake client so you can watch the machinery without an
API key.

| Example | What it demonstrates |
|---|---|
| [`healthcheck`](https://github.com/zarldev/zarlmono/tree/main/examples/healthcheck) | A world-verifying goal: the agent probes a fake server farm until every endpoint reports healthy. Schema + fan-out guardrails police the calls. |
| [`releasegate`](https://github.com/zarldev/zarlmono/tree/main/examples/releasegate) | Pre/post guardrails around a release process: the agent may only publish after every required check is green, and the goal confirms the publish actually happened. |
| [`hnupvote`](https://github.com/zarldev/zarlmono/tree/main/examples/hnupvote) | Live browser automation under pursue: a real Chrome session where the oracle is verified world state and a login wall forces the re-drive path. |
| [`computer_use`](https://github.com/zarldev/zarlmono/tree/main/examples/computer_use) | Live observe → model → act loop using typed computer-use tools. Requires Chrome, network access, and an LLM. |
| [`spawn_worker`](https://github.com/zarldev/zarlmono/tree/main/examples/spawn_worker) | Hierarchical decomposition: a coordinator launches researcher / reviewer / coder tasks with `agent_spawn`, continues independently, and collects results explicitly under work-mode gates and a depth cap. |
| [`stuck_recovery`](https://github.com/zarldev/zarlmono/tree/main/examples/stuck_recovery) | The decompose guardrail's graduated response — pass, advisory, fatal — as an agent repeats a failing search, then recovers by delegating. |
| [`long_conversation`](https://github.com/zarldev/zarlmono/tree/main/examples/long_conversation) | Compactor integration: a pressure-gated compactor keeps a long exploration inside the context window without orphaning tool calls. |
| [`shared_infra`](https://github.com/zarldev/zarlmono/tree/main/examples/shared_infra) | No-LLM tour of code understanding, retrieval indexing/search, workflow graph execution, checkpoint saving, and a deterministic HITL review decision. |
| [`hitl_resume`](https://github.com/zarldev/zarlmono/tree/main/examples/hitl_resume) | Real checkpoint/review/resume boundary: save state, approve/deny/edit through HITL, reload, and invoke an explicit continuation. |
| [`local_mcp`](https://github.com/zarldev/zarlmono/tree/main/examples/local_mcp) | Local stdio MCP subprocess: initialize, discover and call a tool, then disconnect cleanly. |
| [`dynamic_tools`](https://github.com/zarldev/zarlmono/tree/main/examples/dynamic_tools) | Dynamic tool lifecycle: register from disk, invoke, reload, unregister, and reject a built-in name collision. |
| [`skill_catalog`](https://github.com/zarldev/zarlmono/tree/main/examples/skill_catalog) | Discover `SKILL.md` guides, load the versioned store, and select applicable skill content. |
| [`notify_drain`](https://github.com/zarldev/zarlmono/tree/main/examples/notify_drain) | Live notification subscription plus deterministic offline drain after unsubscribe. |
| [`sandboxed_shell`](https://github.com/zarldev/zarlmono/tree/main/examples/sandboxed_shell) | Actual Linux Landlock enforcement: workspace writes succeed while an ungranted outside write is denied. |
| [`deterministic_trace`](https://github.com/zarldev/zarlmono/tree/main/examples/deterministic_trace) | Runner lifecycle callbacks persisted as JSONL and read back without a provider. |

## Running them

```sh
# deterministic, no LLM
go run -C examples ./healthcheck -scripted

# shared infra tour, no LLM or external services
go run -C examples ./shared_infra

# live provider — flags are uniform across agent examples
go run -C examples ./healthcheck -provider anthropic -model claude-sonnet-4-6
go run -C examples ./releasegate -provider llamacpp
```

Provider selection goes through the same
[backends registry](/zarlmono/providers/#the-backends-registry) as
everything else: `-provider` names a builtin, keys come from the
standard env vars, `-base-url` (or `LLAMACPP_BASE_URL` etc.)
overrides endpoints.

## Reading order

Start with `shared_infra` for the non-LLM infrastructure pieces, then `healthcheck` — it's the smallest complete loop with a
real goal. Then `releasegate` for guardrails as policy, and
`stuck_recovery` for what graduated advisories look like in
practice. `hnupvote` is the one to read when you want to see pursue
driving something genuinely flaky (a browser) to a verified outcome.

Each example's README walks through its harness construction;
`examples/patterns.md` in the repo collects the recurring shapes —
scripted clients, fake filesystems, oracle design — you'll reuse in
your own harnesses.
