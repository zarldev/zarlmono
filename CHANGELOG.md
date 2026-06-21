# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [v0.1.0] — 2025-XX-XX

### Initial release

First public release of `zarlmono` — the Zarldev monorepo.

#### Modules

| Module | Tag | What it is |
|---|---|---|
| `zkit` | `zkit/v0.1.0` | Shared library: agent runner, LLM providers, tool system, guardrails, compaction, MCP, cache, filesystem, HTTP/RPC, logging, notifications, sync primitives |
| `zarlcode` | `zarlcode/v0.1.0` | Terminal coding agent / TUI — plan first, execute second, rewind anytime |
| `zarlai` | — | Smart-home/multimodal assistant (excluded from standard CI; CGO deps) |
| `swebench-eval` | `swebench-eval/v0.1.0` | SWE-bench evaluation driver |
| `examples` | — | Deterministic harness demos (not a consumer module) |

#### zarlcode

- **Plan/Build modes** — `Shift+Tab` toggles read-only Plan (investigation) and full Build (execute) modes
- **Session persistence** — sessions saved to `~/.zarlcode/state.db`; resume with `-continue`
- **Headless mode** — `--headless --prompt-file task.md` for CI, scripts, eval harnesses
- **Self-upgrade** — `zarlcode upgrade` downloads and replaces the binary
- **Release pipeline** — `task zarlcode:release VERSION=vX.Y.Z` tags and pushes
- **Settings system** — workspace/global scope, promote (Ctrl+G), inline save feedback, storage inspector
- **Provider support** — anthropic, openai, deepseek, gemini, google-vertex, llamacpp, ollama, claude-code (OAuth), openai-codex (OAuth)
- **File tools** — read, write, edit, apply_patch, grep, glob — workspace-bounded and tracked
- **Shell tools** — foreground (600s max) and background modes with guardrail policies
- **MCP servers** — stdio and HTTP transports; tools register on the flat tool list
- **Sub-agents** — parallel dispatch with mode enforcement (explore/verify/implement)
- **Compaction** — structural, summary, and executive strategies for long sessions
- **Skills** — hot-reloadable capability guides from workspace/home/source-tree directories
- **Theme system** — palette, JSON loader, live-preview gallery in settings

#### zkit (shared library)

- **Agent runner** — `think → call tools → observe → repeat` loop with streaming, compaction, truncation, steering
- **LLM providers** — OpenAI, Anthropic, Google Gemini, DeepSeek, llama.cpp, Ollama, Claude Code (OAuth), OpenAI Codex (OAuth)
- **Tool system** — typed handlers with reflected JSON Schema, registry, MCP bridge, code tools, fetch, search, dynamic registration
- **Guardrails** — pre/post tool-call validation, shell policy, fan-out caps, schema validation
- **Compaction** — structural trimming, LLM summaries, adaptive pressure handling
- **Stability tiers** — core/stable, shared/stable-ish, beta/evolving, experimental/volatile (documented in `zkit/README.md`)
- **Infrastructure** — cache, docstore, filesystem, messagebus, vectorstore, skills, notifications, sync primitives

#### swebench-eval

- Standalone SWE-bench evaluation driver that shares the same agent assembly as `zarlcode` via `zkit/agent/coderunner`

#### Bug fixes

- Fixed claudecode inline `<assistant_tool_calls>` emitted as text ([#1])
- Fixed recovery interceptor panic propagation
- Fixed golangci-lint exclusion rules across all modules
- Added gosec G101/G204 exclusions for specific files
- Fixed cache_prompt gating to llama.cpp backends only

#### Documentation

- Comprehensive AGENTS.md files in all major packages
- zkit README with package map, stability tiers, dependency policy
- Contributing guide with workflow, style, and gotchas
- Documentation site (Astro Starlight → GitHub Pages)

---

## [v0.1.2] — 2025-06-21

### Added

- **go install support** — all `replace` directives stripped from submodule `go.mod` files; `go.work` handles local resolution, module proxy handles remote installs. `go install github.com/zarldev/zarlmono/zarlcode/cmd@v0.1.2` works.

### Changed

- **Dependency pinning** — all modules pin internal dependencies to published versions (`zkit v0.1.2`, `zarlcode v0.1.2`) instead of pseudo-versions with `replace` directives.
- **Release pipeline** — builds output to `dist/` to avoid directory conflicts; Windows dropped from cross-compile matrix (Unix syscall deps).

### Fixed

- CI: `go build ./...` in zarlcode excludes `./cmd` (main package output conflicts with `cmd/` directory)
- Release pipeline: YAML syntax errors resolved, all 4 platforms publish correctly
- Upgrade source: defaults to `zarldev/zarlmono` (was a local path)

## [v0.1.1] — 2025-06-21

### Fixed

- Release pipeline artifacts published to GitHub Releases for linux/{amd64,arm64} + darwin/{amd64,arm64}
- `zarlcode upgrade` works from GitHub Releases
- CI pipeline passes all 10 checks

### Changed

- Release matrix: 4 platforms (dropped windows/amd64 — Unix syscall dependencies)

## [v0.1.0] — 2025-06-18

### Added

- Initial public release of the Zarldev monorepo

[v0.1.2]: https://github.com/zarldev/zarlmono/releases/tag/zarlcode/v0.1.2
[v0.1.1]: https://github.com/zarldev/zarlmono/releases/tag/zarlcode/v0.1.1
[v0.1.0]: https://github.com/zarldev/zarlmono/releases/tag/zarlcode/v0.1.0