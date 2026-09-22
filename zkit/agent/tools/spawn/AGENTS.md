# AGENTS.md — `zkit/agent/tools/spawn`

Notes for editors. See [`zkit/agent/runner/AGENTS.md`](../../runner/AGENTS.md) for the loop this tool calls back into; `taskscope.DepthFrom(ctx)` is the only runner-internal helper it depends on.

## What this package is

A registry-compatible asynchronous agent-task family:

- `agent_spawn` validates and starts a focused child `runner.Run`, then returns a receipt immediately;
- `agent_await` is an explicit blocking join/result-read boundary, not a requirement for receiving results on an automatically admitting parent;
- `agent_status` reads one snapshot without waiting;
- `agent_stop` cancels and joins one child;
- `list_agent_tasks` returns the stable turn-owned task list.

One concrete `Group` owns every child goroutine, cancel function, terminal result, observation state, and shutdown wait path. The composition root creates one group per top-level turn, shares it with recursively built runners, and closes it before releasing runner dependencies.

## Why spawn is its own package

The runner remains synchronous and tool-agnostic. Async delegation policy, recursion limits, result retention, and lifecycle ownership belong in this optional tool package; consumers that do not register it pay no cost.

## Tool protocol invariant

Every `agent_spawn` call returns exactly one immediate tool result paired to its original call ID. A parent composed with `runner.WithInputSource(group)` can receive one automatic typed host observation per child after successful history admission. Explicit `agent_await`, `agent_status`, and `agent_stop` reads remain legal, including rereads. Hosts without that receiving capability retain explicit-only delivery. Never emit a delayed second result for the spawn call or fabricate a human message/tool execution. The provider wire role does not erase neutral host provenance in canonical history.

`Ready` is a non-consuming, parent-scoped readiness snapshot; `Admit` acknowledges references only after the receiving runner has incorporated the corresponding content into history. Wrapper references travel out of band and survive only output-preserving paths, including recoverable failure envelopes. Observation by a tool is not automatic admission. Buffered history admission is not a durable-delivery promise. Pending automatic results remain retained, and cross-parent explicit inspection cannot consume the owner's automatic result.

## Depth tracking

The runner plants task depth in context; `agent_spawn` reads it at execution time. Never store per-call depth on the singleton tool. A configured ceiling of 1 permits one delegation hop and prevents unbounded recursive fan-out.

## Group lifecycle

`NewGroup` starts no goroutine. `Bind` registers a parent run before dispatch. `Start` records a RUNNING task before launching its owned goroutine; children retain dispatch values but survive dispatch-call cancellation, remaining bound to parent cancellation/deadlines. `ParentScope.Close` seals admission, stops and joins its cancellation callback, and cancels/joins descendants before borrowed dependencies can be released. `Finish` atomically seals a genuinely completed parent. A task transitions exactly once to COMPLETED, FAILED, or CANCELLED. Group `Close` seals all scopes, cancels live children, and joins owned work within the caller's cleanup context. Unbound library callers retain the legacy group-owned lifetime.

Public snapshots contain compact immutable result values, never the runner's mutable history slices, channels, contexts, or cancel functions. Group maps store task and parent-state values, mutated under the group lock; waiter/lifecycle snapshots are values as well. Terminal status/await/stop delivery marks the summary observed; listing does not. Omitted-ID lookup is limited to direct children for a bound caller; explicit IDs and listing still support intentional tree inspection.

## Failures are recoverable

Validation, recursion, resolution, workspace-coordination, and admission failures return `Success:false` with a typed tool error and nil Go error so the model can recover. A failed child retains its useful partial summary.

## Agent resolution and work modes

`WithAgentResolver` maps authored names from `list_agents` to runners. Unknown names soft-fall back to the parent runner with a visible notice. The optional planner chooses only from the closed candidate set.

Explore and verify modes are enforced through `WithModeToolPolicy`; implement retains the full surface. Workspace coordination is automatic per tool call: known file paths and every `apply_patch` path are scoped, while opaque or unsafe operations conservatively use the workspace root. Overlapping calls wait cancellably with FIFO fairness; disjoint calls bypass them.

## Things to never do

- Do not store depth or other per-call state on `Tool`.
- Do not start an untracked `go target.Run(...)`; every child belongs to `Group`.
- Do not detach a bound child from its parent or group shutdown, or prune a terminal result while automatic admission or a waiter still needs it.
- Do not present child output as human steering/current authority or inject synthetic tool results. Automatic host observations require an opted-in receiving route; explicit tools remain the fallback. The zarlcode engine enables automatic delivery for its supported receiving providers and keeps other providers on explicit-only delivery.
- Do not register legacy aliases alongside the resource-first names; one public grammar is the invariant.
