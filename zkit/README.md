# zkit

`zkit` is the ordinary-Go agent substrate extracted from zarlai and used by zarlcode.

It provides reusable contracts and infrastructure for the hard parts that showed up in real products: agent runtime, LLM providers, tool execution, MCP, code/workspace tooling, guardrails, compaction, cache, document store, filesystem, message bus, HTTP/RPC helpers, logging, environment handling, notifications, app lifecycle, and synchronization primitives.

zarlcode is the fastest way to try that substrate against a real repository. zkit is the library layer when you want to build your own agent system without adopting a framework runtime or YAML graph.

Although it lives in the monorepo, `zkit` has its own `go.mod` and is built, tested, linted, and versioned as an independent Go module.

```text
zkit
  ↑
  ├── zarlcode        terminal coding-agent workbench
  ├── zarlai          local multimodal assistant
  ├── swebench-eval   repeatable coding-agent evaluation
  └── examples/tools  deterministic harnesses and demos
```

`zkit` must not import downstream app modules.

---

## Try it

For the product surface, install zarlcode and run it inside a repository:

```bash
brew install zarldev/tap/zarlcode
zarlcode init
zarlcode
```

From a source checkout, use `go tool task zarlcode` to build and install the binary as `zarlcode`.

For the library, add zkit to your Go module:

```bash
go get github.com/zarldev/zarlmono/zkit@latest
```

Run deterministic examples without an LLM key:

```bash
go run -C examples ./shared_infra
go run -C examples ./releasegate -scripted
```

The smallest useful composition is still ordinary Go: a provider/client, a tool registry, and the runner. See the docs site getting-started guide for a complete snippet and the `examples/` module for credential-free harnesses.

---

## Module commands

Run these from the repository root:

```bash
go build -C zkit ./...
go vet -C zkit ./...
go test -C zkit -count=1 ./...
go test -C zkit -race -count=1 ./...
(cd zkit && golangci-lint run)
```

`zkit` is included in the repo CI matrix as its own module.

---

## Consumers

### `zarlcode`

Uses `zkit` for the core coding-agent substrate:

- agent runner and event model;
- guardrails and shell policy;
- conversation compaction;
- LLM provider abstraction and backend registry;
- tool registry and code/workspace tools;
- MCP client/server bridges;
- process, environment, logging, notification, and sync helpers.

### `zarlai`

Uses or should use `zkit` for shared assistant/service infrastructure:

- LLM provider abstraction;
- tool schema/contracts;
- MCP/tool interfaces;
- task, sensor, cache, bus, and notification primitives where generalized.

### `swebench-eval`

Uses or should use `zkit` for repeatable agent evaluation infrastructure:

- runner/harness pieces;
- guarded code tools;
- task scoping and repeat caps;
- deterministic test helpers.

---

## What belongs in zkit

Code belongs in `zkit` when it:

- is needed by two or more modules; or
- defines a canonical shared contract; or
- removes duplicated service infrastructure; and
- is product-neutral;
- can be tested independently;
- does not import downstream app modules.

Code does **not** belong in `zkit` when it is:

- `zarlcode` UI/TUI-specific;
- `zarlai` domain-specific, such as Home Assistant, face, voice, camera, or person flows;
- `swebench-eval` orchestration-specific;
- prompt/config/product-specific unless generalized;
- one-off logic without a shared contract.

When in doubt: keep product behavior app-local and move only the reusable
contract or infrastructure into `zkit`.

---

## Package naming convention

`zkit` uses two naming styles deliberately.

- `z*` packages are small Zarldev infrastructure helpers around common Go/runtime concerns: `zhttp`, `zlog`, `zsync`, `zrpc`, `zapp`, `zenv`, `zexec`, and `znotify`.
- Domain packages use plain descriptive names: `agent`, `ai`, `mcp`, `cache`, `messagebus`, `docstore`, `filesystem`, `skills`, and `vectorstore`.
- Protocol names are not branded: use `mcp`, not `zmcp`.

Do not rename clear domain packages just to add a `z` prefix.

---

## Stability tiers

These tiers document how downstream modules should treat each package. They are
not a Go compatibility guarantee by themselves; they are the monorepo governance
rules for where churn is acceptable.

### Core / stable

Small, foundational packages that should remain boring and dependency-light:

```text
options
processenv
zapp
zenv
zexec
zhttp
zlog
znotify
zrpc
zsync
mcp
```

### Shared / stable-ish

Canonical shared contracts and runtime pieces. APIs may evolve, but changes
should be deliberate and coordinated with downstream consumers:

```text
ai/llm
ai/tools
ai/tools/toolkit
agent/compact
agent/guardrails
agent/runner
ai/retrieval
cache
messagebus
```

### Beta / evolving

Useful shared packages that are still shaped by downstream product pressure:

```text
agent/checkpoint
agent/diffrecorder
agent/hitl
agent/pursue
agent/retrieval
agent/sandbox
agent/scheduler
agent/sensor
agent/trace
agent/tools/spawn
agent/workflow
ai/tools/code
ai/tools/fetch
ai/tools/search
docstore
filesystem
vectorstore/qdrant
```

### Experimental / volatile integration

Packages that are useful but integrate with volatile surfaces or execute
higher-risk workflows:

```text
ai/tools/dynamic
ai/llm/claudecode
ai/llm/openaicodex
docstore/mongodb
filesystem/seaweedfs
```

`ai/llm/openaicodex` and `ai/llm/claudecode` are marked volatile because their
OAuth-backed product surfaces are less stable than official API providers, not
because the local implementation is considered low quality.

---

## Package map

### Agent runtime

| Package | Purpose |
|---|---|
| `agent/runner` | Core agent loop: conversation lifecycle, streaming, tool dispatch, compaction, truncation, steering, and events. |
| `agent/guardrails` | Pre/post tool-call validation and policy: schema checks, shell policy, fan-out, decomposition, improvement loop, test-edit guidance, and related safeguards. |
| `agent/compact` | Conversation compaction strategies: structural trimming, LLM summaries, adaptive pressure handling, and executive orchestration. |
| `agent/coderunner` | Production code-agent toolset assembly shared by TUI, headless, and eval surfaces. |
| `agent/mcp` | Bridge formatting MCP server-pushed notifications into the runner's inject queue. |
| `agent/checkpoint` | Transport-neutral run checkpoints plus in-memory storage. |
| `agent/hitl` | Human-in-the-loop review requests, decisions, risk levels, and approval policies. |
| `agent/retrieval` | Agent-facing retrieval adapters: prompt-context rendering and retriever-as-tool. |
| `agent/trace` | Normalized trace events plus runner/workflow exporters such as JSONL. |
| `agent/workflow` | Typed graph/workflow composition with static edges, conditional routing, events, and graph-as-tool. |
| `agent/profile` | Code-defined agent execution profiles: persona prompt prefix, model, iteration budget. |
| `agent/pursue` | Deterministic re-drive harness for oracle-backed agent attempts. |
| `agent/sandbox` | Kernel-enforced shell confinement: Landlock filesystem allow-list plus optional empty network namespace. |
| `agent/scheduler` | Cron-backed scheduled task execution abstractions. |
| `agent/sensor` | Polling/reactive sensor abstraction for ambient observations. |
| `agent/shellpolicy` | Shell-command policy validation for agent-executed commands. |
| `agent/sourcechain` | Tool-source wrapper pipeline combinator. |
| `agent/taskscope` | Context-carried task metadata. |
| `agent/tools/repeatcap` | Repeat-call limiting helpers. |
| `agent/tools/spawn` | Spawn sub-task tooling and spawn planning. |

### AI, LLMs, and tools

| Package | Purpose |
|---|---|
| `ai/llm` | Narrow provider contract, completion request/chunk types, response-format and tool-call structures. |
| `ai/llm/backends` | Provider registry and backend config/build helpers. |
| `ai/llm/openai` | OpenAI and OpenAI-compatible provider implementation. |
| `ai/llm/anthropic` | Anthropic/Claude provider implementation. |
| `ai/llm/google` | Gemini provider implementation. |
| `ai/llm/deepseek` | DeepSeek provider facade. |
| `ai/llm/llamacpp` | llama.cpp OpenAI-compatible provider facade. |
| `ai/llm/ollama` | Ollama OpenAI-compatible provider facade. |
| `ai/llm/claudecode` | OAuth-backed Claude product integration. |
| `ai/llm/openaicodex` | OAuth-backed ChatGPT/Codex product integration. |
| `ai/llm/providertest` | Provider conformance test harness. |
| `ai/llm/repair` | Tool-call JSON repair helpers. |
| `ai/llm/templates` | Chat-template metadata and thinking-tag helpers. |
| `ai/retrieval` | Dependency-light RAG primitives: documents, chunking, embedders, vector stores, retrievers, rerankers, in-memory search, and indexing pipeline. |
| `ai/tools` | Tool registry, tool-call/result types, typed handlers, schemas, redaction, fallback, and MCP bridge. |
| `ai/tools/code` | Workspace-scoped file, patch, shell, process, and plan tools. |
| `ai/tools/dynamic` | Runtime dynamic/binary tool registration and MCP connection tools. |
| `ai/tools/fetch` | HTTP/browser-backed web fetch tool. |
| `ai/tools/search` | SearXNG-backed web search tool. |
| `ai/tools/toolkit` | Typed tool builder and schema generation helpers. |

### Shared service infrastructure

| Package | Purpose |
|---|---|
| `mcp` | Model Context Protocol client/server and transports. |
| `cache` | Generic cache interfaces plus memory/file/Redis implementations. |
| `docstore` | Typed document-store abstraction with memory/MongoDB implementations. |
| `filesystem` | File-system abstraction with memory, OS, and SeaweedFS backends. |
| `messagebus` | Typed pub/sub bus with memory and NATS implementations. |
| `vectorstore/qdrant` | Qdrant vector-store client. |
| `skills` | Versioned, hot-reloadable skill store for prompt assembly. |

### Runtime helpers

| Package | Purpose |
|---|---|
| `options` | Canonical functional options type: `Option[T] func(*T)`. |
| `processenv` | Minimal environment construction for child processes. |
| `tui/theme` | Charm-free theme palette and JSON theme loader. |
| `zapp` | CLI/app lifecycle wrapper with cancellation, cleanup, and panic handling. |
| `zenv` | Typed environment-variable readers with defaults. |
| `zexec` | Process execution helpers. |
| `zhttp` | HTTP client/server helpers, retrying client, JSON responses, and middleware/auth subpackages. |
| `zlog` | Shared `slog` setup helpers. |
| `znotify` | Session-keyed notifications with offline queueing. |
| `zrpc` | ConnectRPC middleware and h2c helpers. |
| `zsync` | Thread-safe generic maps, sets, queues, and synchronization primitives. |

---

## Dependency policy

`zkit` intentionally remains one module. Do not add nested `go.mod` files unless
there is a proven need and an explicit plan.

Dependency rules:

- keep core packages dependency-light;
- isolate cloud/browser/database SDKs in specific adapter packages;
- prefer interfaces in core packages and concrete adapters in integration packages;
- use `go mod why -m <module>` before adding major dependencies;
- avoid app-specific dependencies in `zkit`.

Known dependency-heavy areas include:

```text
ai/llm/anthropic
ai/llm/google
ai/llm/openai
ai/tools/fetch
cache/redis
docstore/mongodb
filesystem/seaweedfs
messagebus/nats
vectorstore/qdrant
```

If dependency pressure becomes painful, first consider package-level adapter
isolation. Do not split modules by default.

Likely future adapter-split candidates, if dependency pressure justifies it:

```text
cache/redis
docstore/mongodb
filesystem/seaweedfs
messagebus/nats
```

Keep the current package APIs stable for now; split adapters only with a
coordinated migration plan for downstream modules.

---

## Security and trust boundaries

`zkit` includes process-capable, filesystem-mutating, browser-backed, and
network-capable building blocks. It is not a sandbox.

Important boundaries:

- `ai/tools/code` can read/write workspace files and execute commands depending on the configured toolset and policies.
- Process-capable tools execute with the OS user's privileges.
- MCP stdio transports run local binaries and should be treated as local code execution.
- MCP HTTP and web fetch/search tools can expose requested content to models.
- Browser-backed fetch may launch Chrome/Chromium via `chromedp`.
- Dynamic tools compile and execute local binaries.
- Secret redaction is best-effort defense-in-depth, not a guarantee.

Downstream apps must choose which tools to expose and which guardrails/policies
to apply for their threat model.

---

## Release and versioning

`zkit` is a Go submodule inside the monorepo. Release tags should use the
submodule tag form:

```text
zkit/vX.Y.Z
```

Before `v1`, APIs may evolve. Stable-tier packages should avoid unnecessary
breaking changes, and downstream migrations should happen in the same monorepo
PR when practical. Experimental/volatile integrations may change faster.
