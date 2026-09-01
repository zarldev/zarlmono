# AGENTS.md — `zkit/agent/tools/spawn`

Notes for editors. See [`zkit/agent/runner/AGENTS.md`](../../runner/AGENTS.md) for the loop this tool calls back into; `taskscope.DepthFrom(ctx)` is the only runner-internal helper it depends on.

## What this package is

A registry-compatible asynchronous agent-task family:

- `agent_spawn` validates and starts a focused child `runner.Run`, then returns a receipt immediately;
- `agent_await` is the explicit blocking join and result-delivery boundary;
- `agent_status` reads one snapshot without waiting;
- `agent_stop` cancels and joins one child;
- `list_agent_tasks` returns the stable turn-owned task list.

One concrete `Group` owns every child goroutine, cancel function, terminal result, observation state, and shutdown wait path. The composition root creates one group per top-level turn, shares it with recursively built runners, and closes it before releasing runner dependencies.

## Why spawn is its own package

The runner remains synchronous and tool-agnostic. Async delegation policy, recursion limits, result retention, and lifecycle ownership belong in this optional tool package; consumers that do not register it pay no cost.

## Tool protocol invariant

Every `agent_spawn` call returns exactly one immediate tool result paired to its original call ID. The eventual child summary is delivered by a later `agent_await`, `agent_status`, or `agent_stop` call. Never emit a delayed second result for the spawn call.

## Depth tracking

The runner plants task depth in context; `agent_spawn` reads it at execution time. Never store per-call depth on the singleton tool. A configured ceiling of 1 permits one delegation hop and prevents unbounded recursive fan-out.

## Group lifecycle

`NewGroup` starts no goroutine. `Start` records a RUNNING task before launching its owned goroutine. A task transitions exactly once to COMPLETED, FAILED, or CANCELLED. `Close` rejects starts, cancels live children, and waits for all of them within the caller's context.

Public snapshots contain compact immutable result values, never the runner's mutable history slices, channels, contexts, or cancel functions. Terminal status/await/stop delivery marks the summary observed; listing does not.

## Failures are recoverable

Validation, recursion, resolution, workspace-coordination, and admission failures return `Success:false` with a typed tool error and nil Go error so the model can recover. A failed child retains its useful partial summary.

## Agent resolution and work modes

`WithAgentResolver` maps authored names from `list_agents` to runners. Unknown names soft-fall back to the parent runner with a visible notice. The optional planner chooses only from the closed candidate set.

Explore and verify modes are enforced through `WithModeToolPolicy`; implement retains the full surface. Workspace coordination is automatic per tool call: known file paths and every `apply_patch` path are scoped, while opaque or unsafe operations conservatively use the workspace root. Overlapping calls wait cancellably with FIFO fairness; disjoint calls bypass them.

## Things to never do

- Do not store depth or other per-call state on `Tool`.
- Do not start an untracked `go target.Run(...)`; every child belongs to `Group`.
- Do not detach a child from group shutdown or delete a terminal result before it can be observed.
- Do not inject a child summary as a fake user steering message; explicit agent tools own delivery.
- Do not register legacy aliases alongside the resource-first names; one public grammar is the invariant.
