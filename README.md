[![ci](https://github.com/zarldev/zarlmono/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/zarldev/zarlmono/actions/workflows/ci.yml)
[![Go 1.26](https://img.shields.io/badge/go-1.26-00ADD8.svg)](https://go.dev/)
[![license MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

# zarlmono

`zarlmono` is the home of **`zkit`** — a toolkit of small, independent Go
packages for building AI applications: an agent loop, a tool system,
guardrails, history compaction, an LLM provider layer, and the
infrastructure underneath. The tools in this repo (`zarlcode`, `zarlai`,
`swebench-eval`) are all built with it.

## Modules

`go.work` joins six Go modules:

| Path | Module | Purpose |
|---|---|---|
| `zkit/` | `github.com/zarldev/zarlmono/zkit` | **The toolkit.** Agent runner, LLM providers, tools, guardrails, compaction, MCP, plus the foundation packages (cache, filesystem, HTTP/RPC/logging, notifications, sync primitives). |
| `zarlcode/` | `github.com/zarldev/zarlmono/zarlcode` | Terminal coding agent/TUI, built on `zkit`. |
| `zarlai/` | `github.com/zarldev/zarlmono/zarlai` | Smart-home/multimodal assistant, built on `zkit`. Excluded from normal CI because of CGO/system dependencies. |
| `swebench-eval/` | `github.com/zarldev/zarlmono/swebench-eval` | SWE-bench evaluation driver; builds its agent through the same shared assembly as `zarlcode`. |
| `examples/` | `github.com/zarldev/zarlmono/examples` | Small runnable harnesses, each isolating one `zkit` pattern. |
| `.` | `github.com/zarldev/zarlmono` | Root module: repository tooling and workspace coordination. |

## Repository layout

```
zkit/           Shared libraries and canonical contracts (the substrate)
zarlcode/       Coding-agent TUI and CLI (built on zkit)
zarlai/         Assistant application backend/frontend (built on zkit)
swebench-eval/  SWE-bench evaluation driver (built on zkit)
examples/       Embedded harness and shared-runner examples (own Go module)

site/           Documentation site (Astro Starlight → GitHub Pages)
docker/         Local service definitions, including SearXNG
```
## Quick start

### Use `zkit` in your own code

A complete agent is a provider, a tool registry, and the loop — take those
three and ignore everything else. A tool is a two-method interface; its JSON
schema is reflected from the args struct, so there's nothing to hand-write.

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm/anthropic"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type weatherArgs struct {
	City string `json:"city" doc:"City to report the weather for"`
}

type weather struct{}

func (weather) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "weather",
		Description: "Report the weather for a city.",
		Parameters:  tools.SchemaFor[weatherArgs](), // schema reflected from the struct
	}
}

func (weather) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	city := call.Arguments.String("city", "")
	return &tools.ToolResult{Success: true, Data: city + ": sunny, 21C"}, nil
}

func main() {
	prov, err := anthropic.NewProvider(os.Getenv("ANTHROPIC_API_KEY"))
	if err != nil {
		log.Fatal(err)
	}

	r := runner.New(runner.ClientFromProvider(prov),
		runner.WithTools(tools.NewRegistry(weather{})),
		runner.WithMaxIterations(8),
	)

	res := r.Run(context.Background(), runner.TaskSpec{Prompt: "What's the weather in Oslo?"})
	fmt.Println(res.FinalContent)
}
```

Swap `anthropic.NewProvider` for `llamacpp.NewProvider` (or openai, gemini,
deepseek) and the loop is unchanged. Add guardrails and compaction the same
way — as options on `runner.New`. See [`zkit/README.md`](zkit/README.md).

### zarlcode

Or just run the bundled agent built on those pieces:

```bash
go tool task zarlcode
zarlcode init
zarlcode keys set <provider>
zarlcode
```

Supported LLM providers include `anthropic`, `openai`, `deepseek`, `gemini`,
`llamacpp`, `ollama`, plus OAuth-backed `claude-code` and `openai-codex`.

Common commands:

```bash
zarlcode                               # Launch interactive TUI
zarlcode -continue                     # Resume last session
zarlcode --headless --prompt-file t.md # Run one task without the TUI
zarlcode keys list                     # View stored provider keys, masked
```

### Building blocks and deterministic harnesses

`zkit` is the reusable substrate. The root Taskfile exposes focused checks for
the runner, deterministic harness, coding loop, LLM providers, and tools:

```bash
go tool task foundation:test
go tool task examples:test
```

The `examples/` tree contains small deterministic harnesses built from those
same blocks (`healthcheck`, `releasegate`, and `hnupvote`).

See [`zkit/README.md`](zkit/README.md) for package tiers, dependency policy,
and release/versioning notes. The applications built on zkit have their own READMEs:
[`zarlcode/README.md`](zarlcode/README.md),
[`zarlai/README.md`](zarlai/README.md),
[`examples/README.md`](examples/README.md).

### sweeval

Install the SWE-bench evaluation tool:

```bash
go tool task sweeval
```

### zarlai

`zarlai` is the assistant app with speech, vision, tools, sensors, and a React
frontend. It has its own task-based workflow:

```bash
go tool task zarlai:setup
go tool task zarlai:up
go tool task zarlai:build
```

See [`zarlai/README.md`](zarlai/README.md) and [`zarlai/AGENTS.md`](zarlai/AGENTS.md)
for current service requirements and development commands.

## Build and test

A plain `go test ./...` only covers the current module. To check the modules
that CI normally covers, use the root Taskfile:

```bash
go tool task check
```

`zarlai` is intentionally omitted from the standard loop because local CGO and
system libraries are required for parts of the app.

## zarlcode at a glance

`zarlcode` is the AI coding-agent surface in this repo. Under the TUI is a
shared deterministic agent substrate: guardrails check tool calls, the runner
streams model output and dispatches tools, compaction manages context pressure,
and sessions persist to SQLite so `-continue` resumes the workspace.

Useful docs:

- [`zarlcode/AGENTS.md`](zarlcode/AGENTS.md) — implementation notes for the TUI/config/storage layer.
- [zarldev.github.io/zarlmono](https://zarldev.github.io/zarlmono) — the zkit documentation site.

## Trust and safety boundaries

This repository contains tools that can execute processes, mutate files, run
browser-backed fetches, connect to MCP servers, and call external LLM APIs.
`zkit` is shared infrastructure, not a sandbox. Each downstream app chooses the
tools, guardrails, policies, and credentials appropriate for its threat model.


## Community

- [`CONTRIBUTING.md`](CONTRIBUTING.md) — development workflow, style, and review expectations.
