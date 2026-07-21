<div align="center">

# zarlmono

**The Go-native agent monorepo — zarlcode / zkit / zarlai**

[![ci](https://github.com/zarldev/zarlmono/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/zarldev/zarlmono/actions/workflows/ci.yml)
[![Go 1.26](https://img.shields.io/badge/go-1.26-00ADD8.svg)](https://go.dev/)
[![license MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![docs](https://img.shields.io/badge/docs-zarldev.github.io%2Fzarlmono-8B5CF6)](https://zarldev.github.io/zarlmono)

</div>

<div align="center">

![zarlcode in action](https://zarldev.github.io/zarlmono/zarlcode-hero2.gif)

</div>

---

## What's here

| Asset | Module | What it does |
|---|---|---|
| **zarlcode** | [`zarlcode/`](zarlcode/) | Terminal coding-agent TUI/CLI — plan, build, switch models, resume sessions, inspect diffs, verify. |
| **zkit** | [`zkit/`](zkit/) | The Go agent substrate: streaming runner, tool registry, LLM providers, guardrails, compaction, MCP, sandboxing, vault. |
| **zarlai** | [`zarlai/`](zarlai/) | Local multimodal/smart-home assistant — sensors, events, gRPC, frontend. |
| **swebench-eval** | [`swebench-eval/`](swebench-eval/) | SWE-bench evaluation driver on the same coding-agent assembly. |
| **examples** | [`examples/`](examples/) | Deterministic harnesses that isolate individual patterns, runnable with no LLM. |

---

## Try zarlcode

```bash
# Homebrew
brew install zarldev/tap/zarlcode

# or build from source
cd zarlmono && go tool task zarlcode

# first run
zarlcode init
zarlcode keys set anthropic "$ANTHROPIC_API_KEY"
zarlcode
```

```bash
zarlcode                               # interactive TUI
zarlcode -continue                     # resume the last session
zarlcode --headless --prompt-file t.md # one-shot for scripts/CI
zarlcode keys list                     # show provider keys (masked)
zarlcode upgrade                       # self-update from GitHub Releases
```

Supported providers: **Anthropic**, **OpenAI**, **DeepSeek**, **Gemini**, **Vertex AI**, **llama.cpp**, **Ollama**, plus OAuth-backed **Claude Code** and **OpenAI Codex** surfaces.

> [!NOTE]
> zarlcode configures model endpoints but doesn't start model servers. Run Ollama, llama.cpp, LM Studio, or another OpenAI-compatible server yourself for local inference.

---

## At a glance

| Mode | Tools | Use case |
|---|---|---|
| **Plan** | Read-only | Investigate the codebase, propose a strategy — no edits, no shell. |
| **Build** | Full tool surface | Read, edit, patch, bash, web, MCP, plans, sub-agents — subject to guardrails. |

The TUI streams everything live: model output, tool calls, command results, diffs, plan state, and the file-change log. Sessions persist locally — `zarlcode -continue` picks up right where you left off.

See the [interface tour](https://zarldev.github.io/zarlmono/zarlcode-interface/) →

---

## Use zkit in your own Go app

```bash
go get github.com/zarldev/zarlmono/zkit@latest
```

A minimal agent is just a provider, a tool registry, and the runner:

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
	args, err := tools.DecodeArgs[weatherArgs](call.Arguments)
	if err != nil {
		return tools.Failure(call.ID, err), nil
	}
	return tools.Success(call.ID, args.City+": sunny, 21C"), nil
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

	res := r.Run(context.Background(), runner.TaskSpec{Prompt: "What is the weather in Oslo?"})
	if res.Err != nil {
		log.Fatal(res.Err)
	}
	fmt.Println(res.FinalContent)
}
```

Swap `anthropic` for `openai`, `gemini`, `deepseek`, `ollama`, or `llamacpp` — the runner stays the same. To run without a key or network:

```bash
go run -C examples ./shared_infra
go run -C examples ./releasegate -scripted
```

Add guardrails, compaction, sandboxing, retrieval, and verified completion as options when you need them.

---

## zkit building blocks

| Layer | Package | Docs |
|---|---|---|
| Runner | [`zkit/agent/runner`](zkit/agent/runner/) | [Streaming tool-calling loop](https://zarldev.github.io/zarlmono/runner/) |
| Tools | [`zkit/ai/tools`](zkit/ai/tools/) | [Registry, schemas, effects, MCP](https://zarldev.github.io/zarlmono/tools/) |
| Guardrails | [`zkit/agent/guardrails`](zkit/agent/guardrails/) | [Schema repair, shell policy, caps, verifier feedback](https://zarldev.github.io/zarlmono/guardrails/) |
| Compaction | [`zkit/agent/compact`](zkit/agent/compact/) | [Keeping long sessions inside context](https://zarldev.github.io/zarlmono/compaction/) |
| MCP | [`zkit/mcp`](zkit/mcp/) | Model Context Protocol client/server |
| Vault | [`zkit/vault`](zkit/vault/) | Encrypted credential storage |
| Vector store | [`zkit/vectorstore`](zkit/vectorstore/) | Embedding + retrieval |
| Docstore | [`zkit/docstore`](zkit/docstore/) | Document storage layer |
| zhttp | [`zkit/zhttp`](zkit/zhttp/) | HTTP client foundation |

[Architecture overview →](https://zarldev.github.io/zarlmono/architecture/)

---

## Repository layout

```
zarlcode/       --- Coding-agent TUI & CLI
zkit/           --- Reusable agent libraries
zarlai/         --- Local assistant backend/frontend
swebench-eval/  --- SWE-bench evaluation driver
examples/       --- Deterministic harnesses & patterns
site/           --- Astro/Starlight docs site
docker/         --- Container setup for eval runs
dist/           --- Release artifacts
```

---

## Build & test

```bash
go tool task check          # build -> vet -> test (examples, zkit, zarlcode, swebench-eval)
go tool task lint           # golangci-lint across CI-covered modules
go tool task race           # zkit race-detector suite
```

```bash
go tool task zarlcode              # build+install to ~/.local/bin
zarlcode

go run ./zarlcode/cmd              # run from source
go run ./zarlcode/cmd -continue    # resume last session
```

> [!TIP]
> `zarlai` is excluded from standard pure-Go checks — parts of it require CGO (dlib/go-face, sherpa-onnx). Use `go tool task zarlai:test` for its focused suite.

---

## Trust boundaries

zarlcode and zkit code tools can execute processes, mutate files, fetch web pages, connect to MCP servers, and call external LLM APIs. Guardrails and sandboxing reduce risk but don't turn your user account into a disposable sandbox. Review tool calls when using powerful models or unfamiliar workspaces.

---

## Community

- [Docs site](https://zarldev.github.io/zarlmono)
- [zarlcode README](zarlcode/README.md)
- [zkit README](zkit/README.md)
- [CONTRIBUTING.md](CONTRIBUTING.md)
- [CHANGELOG.md](CHANGELOG.md)

---

<div align="center">

MIT — [LICENSE](LICENSE)

</div>