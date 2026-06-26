[![ci](https://github.com/zarldev/zarlmono/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/zarldev/zarlmono/actions/workflows/ci.yml)
[![Go 1.26](https://img.shields.io/badge/go-1.26-00ADD8.svg)](https://go.dev/)
[![license MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

# zarlmono

**zarlmono is the monorepo for zarlcode, zkit, zarlai, and the eval/examples that keep them honest.**

- **[`zarlcode`](zarlcode/)** — a terminal coding agent/TUI that reads, edits, runs commands, searches the web, and delegates to sub-agents while keeping sessions local and resumable.
- **[`zkit`](zkit/)** — the reusable Go toolkit underneath: streaming runner, tool registry, provider adapters, guardrails, compaction, MCP, sandboxing, and small foundation packages.
- **[`zarlai`](zarlai/)** — a local multimodal assistant built on the same runner and tool system.

Docs: **[zarldev.github.io/zarlmono](https://zarldev.github.io/zarlmono)**

![zarlcode in action](https://zarldev.github.io/zarlmono/zarlcode-hero2.gif)

## Try zarlcode

```bash
# install the latest tagged CLI
go install github.com/zarldev/zarlmono/zarlcode/cmd@v0.1.4

# or via Homebrew
brew install zarldev/tap/zarlcode

# first run
zarlcode init
zarlcode keys set <provider>   # anthropic, openai, gemini, deepseek, ...
zarlcode
```

Common commands:

```bash
zarlcode                               # interactive TUI
zarlcode -continue                     # resume the last session in this workspace
zarlcode --headless --prompt-file t.md # one non-interactive task for scripts/CI
zarlcode keys list                     # show configured provider keys, masked
```

`zarlcode upgrade` self-updates from GitHub Releases:

```bash
zarlcode upgrade source set zarldev/zarlmono
zarlcode upgrade
```

Supported providers include Anthropic, OpenAI, DeepSeek, Gemini, Google Vertex,
llama.cpp, Ollama, and OAuth-backed Claude Code / OpenAI Codex surfaces.

## Use zkit in your own Go app

```bash
go get github.com/zarldev/zarlmono/zkit@v0.1.3
```

A minimal agent is an LLM provider, a tool registry, and the runner:

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
		Parameters:  tools.SchemaFor[weatherArgs](),
	}
}

func (weather) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	city := call.Arguments.String("city", "")
	return tools.Success(call.ID, city+": sunny, 21C"), nil
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

	res, err := r.Run(context.Background(), runner.TaskSpec{Prompt: "What is the weather in Oslo?"})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.FinalContent)
}
```

Swap the provider for OpenAI, Gemini, DeepSeek, llama.cpp, or Ollama and the
runner code stays the same. Add guardrails, compaction, sandboxing, retrieval,
and verified completion as options when you need them.

## Why this repo exists

Most agent frameworks make the demo easy and the production edges vague.
This monorepo is shaped the other way around: the abstractions are small because
they are shared by multiple real consumers.

- **Terminal-first coding workflow.** zarlcode is not just an example app; it is
  the product surface for the coding agent.
- **Go-native composition.** No framework runtime or YAML graph is required. The
  important contracts are ordinary Go interfaces.
- **Tool execution is explicit.** Tools declare schemas, effects, errors, and
  purity; guardrails wrap dispatch instead of hiding inside prompts.
- **Long runs are expected.** Sessions persist, processes are tracked,
  compaction is built in, and sub-agents isolate exploratory work.
- **Verification beats vibes.** `pursue` can re-drive an agent against real
  world checks such as tests passing or files existing.

## Repository layout

`go.work` joins six Go modules:

| Path | Module | Purpose |
|---|---|---|
| `zarlcode/` | `github.com/zarldev/zarlmono/zarlcode` | Terminal coding agent/TUI and CLI, built on `zkit`. |
| `zkit/` | `github.com/zarldev/zarlmono/zkit` | Shared agent toolkit: runner, providers, tools, guardrails, compaction, MCP, sandboxing, foundation packages. |
| `zarlai/` | `github.com/zarldev/zarlmono/zarlai` | Local multimodal/smart-home assistant. Has CGO/system dependencies and its own CI path. |
| `swebench-eval/` | `github.com/zarldev/zarlmono/swebench-eval` | SWE-bench evaluation driver using the same coding-agent assembly as zarlcode. |
| `examples/` | `github.com/zarldev/zarlmono/examples` | Small runnable harnesses that isolate individual zkit patterns. |
| `.` | `github.com/zarldev/zarlmono` | Root tooling and workspace coordination. |

```
zarlcode/       Coding-agent TUI and CLI
zkit/           Reusable agent libraries and foundation packages
zarlai/         Local assistant backend/frontend
swebench-eval/  SWE-bench evaluation driver
examples/       Deterministic harnesses and patterns
site/           Astro/Starlight documentation site
```

## zarlcode at a glance

zarlcode has two main work modes:

| Mode | What happens |
|---|---|
| **Plan** | Read-only investigation. The agent can inspect files and propose a plan, but cannot edit or run shell commands. |
| **Build** | Full tool surface: read, edit, patch, bash, web, MCP, plans, and sub-agent dispatch, subject to guardrails. |

Useful features:

- workspace-scoped file tools with anchored edits;
- shell process manager for foreground and background commands;
- plan pane, working set, file viewer, model picker, settings, and themes;
- sub-agents with `explore`, `verify`, and `implement` modes;
- resumable SQLite sessions in `~/.zarlcode/state.db`;
- headless mode for scripts and eval harnesses.

See [`zarlcode/README.md`](zarlcode/README.md) and the
[interface tour](https://zarldev.github.io/zarlmono/zarlcode-interface/).

## zkit building blocks

Start with:

- [Architecture](https://zarldev.github.io/zarlmono/architecture/) — package map and dependency direction.
- [Runner](https://zarldev.github.io/zarlmono/runner/) — the streaming tool-calling loop.
- [Tools](https://zarldev.github.io/zarlmono/tools/) — registry, schemas, effects, dynamic tools, MCP.
- [Guardrails](https://zarldev.github.io/zarlmono/guardrails/) — schema repair, shell policy, fan-out caps, verifier feedback.
- [Compaction](https://zarldev.github.io/zarlmono/compaction/) — keeping long sessions inside context.
- [Examples](examples/) — deterministic harnesses, most runnable with no LLM.

## Build and test

A plain `go test ./...` only covers the current Go module. To run the normal
multi-module checks:

```bash
go tool task check
```

Common local commands:

```bash
go tool task zarlcode              # build + install zarlcode to ~/.local/bin
go run ./zarlcode/cmd              # run zarlcode from source
go run ./zarlcode/cmd -continue    # resume last session
```

`zarlai` is intentionally outside the standard pure-Go check loop because parts
of it require CGO libraries for dlib/go-face and sherpa-onnx.

## Trust boundaries

zarlcode and zkit code tools can execute processes, mutate files, fetch web
pages, connect to MCP servers, and call external LLM APIs. Guardrails and
sandboxing reduce risk; they do not turn your user account into a disposable
sandbox. Review tool calls when using powerful models or unfamiliar workspaces.

## Community

- [Documentation site](https://zarldev.github.io/zarlmono)
- [zarlcode README](zarlcode/README.md)
- [zkit README](zkit/README.md)
- [CONTRIBUTING.md](CONTRIBUTING.md)
- [CHANGELOG.md](CHANGELOG.md)

MIT — see [LICENSE](LICENSE).
