# AGENTS.md — `zkit/agent/tools/spawn`

Notes for editors. See [`zkit/agent/runner/AGENTS.md`](../../runner/AGENTS.md) for the loop this tool calls back into; `taskscope.DepthFrom(ctx)` is the only runner-internal helper it depends on.

## What this package is

A registry-compatible `spawn_agent` tool that lets a running agent kick off a focused sub-task in a fresh `runner.Run`, returning only the child's summary as a single tool result. The recursion ceiling is owned by the tool instance, not the runner — each consumer chooses how deep its agent can go. Consumers that don't want sub-agent recursion simply don't register the tool.

## Why spawn is its own package

The runner runs whatever's in the registry; spawn is one possible tool. Keeping it out of the runner package makes the separation honest: the loop stays tool-agnostic, consumers without sub-agents don't import this, and the recursion ceiling lives on the tool instance where different consumers can choose different ceilings without growing the runner's option list.

## Depth tracking

Spawn needs its current depth at `Execute` time to decide whether to refuse. The runner plants the current task's depth on ctx at the top of every `Run` (`taskscope.WithDepth`), and spawn reads it back (`taskscope.DepthFrom`). Context threading is the only correct approach: the spawn tool is a singleton (registered once, invoked across nested Runs), so a depth captured in a closure at construction would be stale.

The flow: top-level `Run(Depth:0)` plants `0`; the agent calls `spawn_agent`, which reads `0`, is below the ceiling, and calls `parent.Run` with `Depth:1`; the child's `Run` plants `1`; and so on. At depth = ceiling, spawn refuses.

## Default ceiling is 1

`defaultMaxDepth` is 1 — the parent can delegate to a sub-agent, but that sub-agent cannot spawn another. Larger cloud models treat `spawn_agent` as a free-form fan-out primitive and burn their context plus the provider's rate limit building deep trees of children whose results never converge. Capping at 1 keeps spawn a single delegation hop (the "researcher" / "code-reviewer" pattern) and forces the parent to flatten deeper work into its own iteration loop, where the runner's guardrails actually fire. `WithMaxDepth(n)` raises it for legitimate supervisor-of-supervisors workloads.

## Failures are recoverable (the `Success:false` path)

When spawn refuses (max depth, missing prompt, runner error), it returns `(*tools.ToolResult{Success:false}, nil)` — a nil error from `Execute`, not a Go error. The runner treats `(*ToolResult, nil)` as a tool reporting failure cleanly (the agent sees the message and continues), and `(_, error)` as an unrecoverable dispatch error that aborts the iteration. Spawn's failures are always the former — the agent should see the refusal and decide what to do.

## Agent resolution and soft fallback

When the `agent` argument is empty or names an unknown agent, spawn soft-falls-back to the parent runner with a one-line notice (captured in the child's summary so the parent sees the rerouting). Hard-erroring would send the model down a "no agents defined → I'll do this manually" detour where it reads every file one at a time, defeating the point of spawn.

`WithAgentResolver` maps agent names to runners — how `zarlcode` exposes one runner per authored agent profile, so a parent on one model can delegate "review this code" to a `code_reviewer` agent backed by another. `WithSpawnPlanner(planner, agents)` adds a grammar-constrained recovery hook, consulted **only** when the model omits or misspells `agent`: the planner picks from a closed enum of registered names via structured output, so it cannot invent a name the resolver won't recognize. An explicit valid pick by the model wins without consulting the planner.

Planners implementing `ProbingPlanner` get a one-time `Probe` health check on the first `applyPlanner` call (guarded by `sync.Once`); a failing probe logs at warn but never aborts a spawn — surfacing misconfiguration even when a healthy run never calls `Plan`.

## Work modes and tool gating

Three modes shape a child's scope and prompt: `explore` (read-only investigation), `implement` (full tool surface), `verify` (review, output a verdict). `WithModeToolPolicy` turns the mode from advisory text into enforced policy: the policy reports whether a tool spec is allowed for a mode, and spawn plants it on the child's ctx via `runner.WithToolGate`, so the runner hides disallowed tools and refuses them if called. An unknown or empty mode triggers no gating.

## Options

`New(parent, opts...)`: `WithMaxDepth(n)` (negative ignored), `WithSpawnMaxIterations(n)` (clamp the child's iteration cap), `WithAgentResolver`, `WithSpawnPlanner`, `WithModeToolPolicy`.

## Things to never do

- **Don't store depth on the tool instance.** It's a singleton used across nested Runs concurrently; per-instance depth would race. Use ctx.
- **Don't make spawn aware of the runner's other tools.** It just calls `parent.Run`; the child sees the same tool source because it's the same Runner.
- **Don't add knobs.** A handful of params and options is the whole API. Richer sub-agent control belongs in a different tool.
