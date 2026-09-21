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
defer group.Close(context.WithoutCancel(ctx))
```

Here `ctx` is the run context. The lifecycle owner creates one group per top-level turn and shares it with named and recursive child runners and the same workspace coordinator. It must cancel and join children before releasing shared dependencies or reporting the turn drained. `Group.Close` can return early when its supplied context is canceled, so the owner must not abandon the dependency-safe join; `context.WithoutCancel(ctx)` prevents prior run cancellation from short-circuiting deferred cleanup.

`spawn.NewAsync(parent, group, opts...).Register(reg)` is the direct registration-only helper. It installs the tool family on `reg` and shares the caller-owned group without taking lifecycle ownership.

## Protocol and completion

Every model tool call needs exactly one paired result before the next completion. `agent_spawn` therefore returns a receipt under the original call ID; it never emits the child summary later under that call.

Without an automatic input source, summaries are read through `agent_await`, terminal `agent_status`, or `agent_stop`; completion guards consider the receiving parent's own outstanding children. Omitted task IDs select only direct children of a bound caller. An explicit read from another parent does not consume the owner's unread or automatic-admission state.

An opted-in parent can use `runner.WithInputSource(group)` to admit one typed host observation per child at a safe history boundary. Bind the root with `group.Bind(runCtx, spec.ID)` before `Run` and close that scope after `Run` returns, before releasing shared dependencies; child scopes are owned by the group. A no-tool parent waits for child results or genuine user input without spending provider attempts. It finishes only after ordinary completion checks accept and admission is atomically sealed. Explicit reads remain available and may intentionally repeat a result. The engine's automatic path is experimental and disabled by default (`engine.WithExperimentalAutomaticCompletion`); qualify the configured receiving endpoints before opting in.

Observation by an explicit tool and admission into model history are distinct. The runner acknowledges trusted references only after history capture succeeds; buffered capture is not a promise of durable storage. Output-preserving wrappers retain these references, while discarded, transformed, or truncated output must not acknowledge a result. Host provenance survives storage, compaction and replay; child summaries remain evidence from their original assignments, not user instructions or proof of current state.

Observed terminal tasks are retained as bounded recent history (32 by default). Running tasks, active waiters, and terminal results still pending automatic admission are pinned. `WithMaxObserved` sets the bound on evictable history; nonpositive disables pruning. `WithMaxRuntime` bounds child lifetime in addition to the owning parent's cancellation/deadline. Runtime exhaustion is reported distinctly as a budget failure.

`list_agent_tasks` is metadata-only and never consumes completion evidence. Its explicit tree inspection does not change the parent-scoped automatic routing contract.

## Lifecycle

```text
RUNNING -> COMPLETED
        -> FAILED
        -> CANCELLED
```

`Group.Close` stops admission, cancels live children, and waits for all owned goroutines within the supplied context. Tasks are turn-scoped and do not survive session restart.

## Depth, fan-out, and work modes

The default depth ceiling is one delegation hop. A separate per-task fan-out guardrail bounds sibling launches.

- `explore` — read-only investigation;
- `verify` — review and bounded verification without file-edit tools;
- `implement` — full tool surface.

Workspace scopes are inferred automatically for every tool call: file tools use `path` or `root`, `apply_patch` coordinates all patch paths, and plan tools use `.zarlcode/plans`. Disjoint paths execute concurrently; overlapping calls wait in fair arrival order until the conflicting call completes. Cancellation removes a queued call cleanly. Opaque operations such as bash, and calls with missing or unsafe paths, conservatively cover the whole workspace.

## Named agents

`WithAgentResolver` maps names returned by `list_agents` to alternate runners. Unknown names soft-fall back to the parent runner with a visible notice. The optional grammar-constrained planner chooses only from registered candidates.
`WithDefaultAgent` can map each work mode to one of those named profiles when the caller omits `agent`; an explicit `agent` always wins.
Per-mode iteration budgets override the shared child budget. `WithMaxConcurrent` bounds simultaneously running children in a group, while `WithFallbackPolicy` selects planner recovery, direct parent fallback, or strict refusal.

See [`AGENTS.md`](AGENTS.md) for package invariants and editor guidance.
