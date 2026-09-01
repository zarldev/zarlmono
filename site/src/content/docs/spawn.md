---
title: Sub-agent tasks
description: Asynchronous agent_spawn delegation with explicit joins, turn-owned lifecycle, depth/fan-out caps, and workspace coordination.
---

`zkit/agent/tools/spawn` provides an asynchronous agent-task family. `agent_spawn`
starts a focused child `runner.Run` and immediately returns a task receipt, allowing
the parent to continue. The eventual summary is delivered by `agent_await`, terminal
`agent_status`, or `agent_stop`; `list_agent_tasks` lists the turn's retained tasks.

## Wiring

```go
group := spawn.NewGroup()
r := runner.New(client, runner.WithTools(reg) /* … */)
coderunner.RegisterSpawnTools(reg, r, group, 1, 0)
defer group.Close(shutdownCtx)
```

Register the family after constructing the parent runner. One concrete `Group` owns
every child goroutine, cancellation function, terminal result, and shutdown wait path.
zarlcode creates one group per top-level turn and shares it with named and recursive
child runners.

## Tool protocol

Every model tool call must receive exactly one paired result before the next model
completion. `agent_spawn` therefore returns a receipt under its original call ID; it
does not defer that result until the child finishes or emit a second result later.

The root completion guard prevents a final answer while a child is running or a
terminal summary remains unobserved. `agent_await` is the deliberate blocking join.

## Lifecycle

```text
RUNNING -> COMPLETED
        -> FAILED
        -> CANCELLED
```

`Group.Close` rejects new starts, cancels running children, and waits for all owned
goroutines within the caller's shutdown context. Agent tasks are turn-scoped and are
not restored after a process restart.

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

The parent can continue independent work, inspect with `agent_status`, and explicitly
join with `agent_await` when it needs the summary.
