# `zkit/agent/tools/spawn`

The `spawn_agent` tool — a registry-compatible tool that lets the
running agent kick off a focused sub-task in a fresh `runner.Run`,
returning only the child's summary as a single tool result.

## Why it's a separate package

Nothing in the runner's loop requires spawn. It is a tool that a
consumer can register when sub-agent recursion is desirable. The
zarlcode binary registers it by default for interactive sessions;
other consumers that don't want sub-agent recursion (zarlai's stateless
one-shot endpoints, say) can simply avoid importing this package and the
runner will have no `spawn_agent` tool.

The recursion ceiling lives on the tool, not on the runner.
Different consumers can choose different ceilings without growing
the runner's option list.

## Quick start

```go
import (
    "github.com/zarldev/zarlmono/zkit/agent/runner"
    "github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
)

r := runner.New(client, runner.WithTools(toolReg), /* opts */)
toolReg.Register(spawn.New(r))               // default ceiling: 1
toolReg.Register(spawn.New(r, spawn.WithMaxDepth(5))) // custom
```

`r` is the parent runner the spawn tool will dispatch children to.
Default ceiling is 1 — the parent task can delegate to a sub-agent,
but that sub-agent cannot recursively spawn another. `WithMaxDepth(0)`
disables spawning entirely (useful for endpoints that want zero
recursion).

## Ordering

Register spawn *after* the runner is constructed — the tool's
constructor needs the parent runner to capture in a closure. The
runner's tool registry is re-snapshotted every iteration, so a tool
registered post-construction is callable on the next turn.

## Key types

- [`Tool`] — the spawn-agent tool struct. Implements `tools.Tool`.
- [`New`] — constructor. `New(parent, opts...)`.
- [`WithMaxDepth`] — option to override the default ceiling.
- [`ToolName`] — the registered tool name (`"spawn_agent"`).

See [`AGENTS.md`](AGENTS.md) for design notes on depth threading and
why this package isn't in `zkit/agent/runner`.

[`Tool`]: https://pkg.go.dev/github.com/zarldev/zarlmono/zkit/agent/tools/spawn#Tool
[`New`]: https://pkg.go.dev/github.com/zarldev/zarlmono/zkit/agent/tools/spawn#New
[`WithMaxDepth`]: https://pkg.go.dev/github.com/zarldev/zarlmono/zkit/agent/tools/spawn#WithMaxDepth
[`ToolName`]: https://pkg.go.dev/github.com/zarldev/zarlmono/zkit/agent/tools/spawn#ToolName
