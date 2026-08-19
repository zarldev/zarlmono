# Sub-agent tasks

`zkit/agent/tools/spawn` provides the asynchronous agent-task tool family:

- `agent_spawn` starts a focused child `runner.Run` and immediately returns a task receipt;
- `agent_await` explicitly waits for and returns a terminal summary;
- `agent_status` inspects one task without waiting;
- `agent_stop` cancels and joins one task;
- `list_agent_tasks` lists the stable tasks owned by the current turn.

The parent can continue reasoning and using non-conflicting tools while children run. Every child belongs to a concrete `Group`; there is no detached `go runner.Run(...)` path.

## Why it is separate

The runner stays synchronous and tool-agnostic. Consumers opt into delegation by constructing one turn-owned `Group`, registering the agent tools after the parent runner exists, and closing the group before releasing runner dependencies.

## Wiring

```go
group := spawn.NewGroup()
r := runner.New(client, runner.WithTools(reg), /* options */)
coderunner.RegisterSpawnTools(reg, r, group, 1, 0)
defer group.Close(shutdownCtx)
```

zarlcode uses one group per top-level turn. Named and recursive child runners share that group and the same workspace coordinator.

## Protocol and completion

Every model tool call needs exactly one paired result before the next completion. `agent_spawn` therefore returns a receipt under the original call ID; it never emits the child summary later under that call.

The summary is delivered through `agent_await`, terminal `agent_status`, or `agent_stop`. Parent completion is guarded while any child is running or any terminal summary remains unobserved.

## Lifecycle

```text
RUNNING -> COMPLETED
        -> FAILED
        -> CANCELLED
```

`Group.Close` stops admission, cancels live children, and waits for all owned goroutines within the caller's shutdown context. Tasks are turn-scoped and do not survive session restart.

## Depth, fan-out, and work modes

The default depth ceiling is one delegation hop. A separate per-task fan-out guardrail bounds sibling launches.

- `explore` — read-only investigation;
- `verify` — review and bounded verification without file-edit tools;
- `implement` — full tool surface.

Explore and verify children hold a shared workspace READ lease for their lifetime. Implement children hold an exclusive WRITE lease. Parent and child tool calls acquire short-lived leases under their task IDs; conflicts return recoverable failures rather than blocking or racing.

## Named agents

`WithAgentResolver` maps names returned by `list_agents` to alternate runners. Unknown names soft-fall back to the parent runner with a visible notice. The optional grammar-constrained planner chooses only from registered candidates.

See [`AGENTS.md`](AGENTS.md) for package invariants and editor guidance.
