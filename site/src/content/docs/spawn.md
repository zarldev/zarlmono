---
title: Sub-agent tasks
description: Asynchronous agent_spawn delegation with automatic parent-model inputs, explicit inspection, turn-owned lifecycle, and workspace coordination.
---

`zkit/agent/tools/spawn` provides an asynchronous agent-task family. `agent_spawn`
starts a focused child `runner.Run` and immediately returns a task receipt, allowing
the parent to continue. With automatic delivery enabled, a completed child's result
arrives directly as input to its parent model at the next safe history boundary;
the parent does not need to poll or call `agent_await` merely to receive it.

zarlcode enables this for OpenAI, Anthropic, Google, llama.cpp, and OpenAI Codex
receiving providers. Other receiving providers retain explicit result collection.
`agent_status`, `agent_await`, and `agent_stop` remain available for intentional
inspection, waiting/rereading, and cancellation. `list_agent_tasks` lists retained
task metadata without consuming results.

## Wiring

```go
group := spawn.NewGroup()
r := runner.New(client, runner.WithTools(reg), runner.WithInputSource(group) /* … */)
coderunner.RegisterSpawnTools(reg, r, group, 1, 0)
defer group.Close(context.WithoutCancel(ctx))
```

Register the family after constructing the parent runner. One concrete `Group` owns
every child goroutine, cancellation function, terminal result, and shutdown wait path.
Here `ctx` is the run context. The lifecycle owner creates one group per top-level turn,
shares it with named and recursive child runners, and must cancel and join those children
before releasing shared dependencies or reporting the turn drained. `Group.Close` can
return early when its supplied context is canceled, so the owner must not abandon the
dependency-safe join; `context.WithoutCancel(ctx)` prevents prior run cancellation from
short-circuiting deferred cleanup.

For direct registration, `spawn.NewAsync(parent, group, opts...).Register(reg)` installs
the tool family while sharing the same caller-owned group; it does not take lifecycle
ownership.

The snippet above shows construction for a receiving provider that supports host
observations. Before `Run`, bind the root with `group.Bind(ctx, spec.ID)` and handle
its error. Close the returned parent scope after `Run` returns, before releasing
shared dependencies; child scopes are group-owned. `WithInputSource(group)` is the
opt-in for standalone zkit applications. Omit it for unqualified receiving routes
and use explicit result reads instead.

## Tool protocol

Every model tool call must receive exactly one paired result before the next model
completion. `agent_spawn` therefore returns a receipt under its original call ID; it
does not defer that result until the child finishes or emit a second result later.

Automatic completion input is a typed host observation, not a delayed second tool
result and not a new instruction from the user. It is routed to the child's owning
parent and remains evidence from the original assignment—not proof of the current
workspace state. Its provenance is preserved through history capture and replay.

A parent with no tool calls waits while its owned work is outstanding, without
spending provider attempts on polling. Completion requires the usual completion
checks and sealed input admission. Explicit `agent_await` calls are optional waits
or rereads on automatic routes; they remain the result-collection path on hosts
without automatic input delivery.

## Lifecycle

```text
RUNNING -> COMPLETED
        -> FAILED
        -> CANCELLED
```

`Group.Close` rejects new starts, cancels running children, and waits for all owned
goroutines within the supplied context. Agent tasks are turn-scoped and are not restored
after a process restart. Observed terminal tasks are pruned to a bounded history by
default; `WithMaxObserved` sets that bound, and a non-positive value disables pruning.

## The two caps

**Depth, default 1.** A parent may delegate, but its child cannot spawn a grandchild.
`WithMaxDepth(0)` disables registration.

**Fan-out.** A separate per-task guardrail limits sibling `agent_spawn` calls. Depth
prevents recursive trees; fan-out prevents one parent from launching an unbounded
number of children.

## Named agents

`spawn.WithAgentResolver` routes an `agent="reviewer"` argument to another runner
with its own provider, model, prompt, and tool gates. Names come from `list_agents`.
Unknown names soft-fall back to the parent runner with a visible notice. The optional
`SpawnPlanner` chooses only from the registered candidate set.

## Work modes and workspace coordination

- **`explore`** — read-only investigation;
- **`verify`** — review and bounded verification without file-edit tools;
- **`implement`** — full tool surface.

Workspace scopes are inferred automatically for each tool call. File tools use their
`path` or `root`; `apply_patch` coordinates every path in the patch; plan tools use
`.zarlcode/plans`. Disjoint paths may execute concurrently, while equal or
ancestor/descendant paths conflict. Operations whose effects cannot be bounded—such
as shell commands—or calls with missing/unsafe paths conservatively cover the whole
workspace. Overlapping calls wait in fair arrival order and wake when the blocking
lease is released; disjoint paths continue concurrently. Cancellation or deadlines
remove a queued call cleanly.

## What the child sees

- the prompt supplied by the parent, without inherited conversation history;
- the same live tool source, filtered by work mode;
- its own iteration budget and task identity.

The parent can continue independent work and respond when the child's completion
arrives automatically. Use `agent_status` for an intentional status check, or
`agent_await` when an explicit wait or reread is useful—not merely to trigger
delivery on an automatic route.
