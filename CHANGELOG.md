# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
## [zkit/v0.4.0] — 2026-07-12

`zkit/v0.4.0`

### Added

- Provider-neutral `program` tool support for bounded, read-only Starlark fan-out over existing guarded tool sources.
- Programmatic read policy wiring for coderunner, including explicit allowlists for read/search/catalogue tools and bounded `call`, `call_many`, and `emit` execution.
- OpenAI Responses API request/stream handling, model plan metadata, endpoint kind, reasoning effort, and token-limit support.
- Session message-count storage migration and query plumbing.

### Changed

- Runner and guardrail event/context accounting now carries richer task/tool metadata for fan-out and context-breakdown reporting.
- Structured tool results normalize cleanly through programmatic execution while preserving typed result rendering for callers.
- OpenAI and Codex model metadata and request construction were refreshed for current endpoint capabilities.

### Fixed

- Hashline parsing and rendering edge cases are covered with broader tests.
- Dynamic MCP/computer tool registration now handles registry errors explicitly.

## [zarlcode/v0.4.0] — 2026-07-12

`zarlcode/v0.4.0`

### Added

- Opt-in `programmatic_tools` setting that exposes a portable `program` tool for read/search/catalogue fan-out while keeping edit/write/bash actions direct.
- Prompt guidance and tool-roster filtering for programmatic read/search workflows.
- Compact TUI rendering for program results, including call summaries and known structured-result summaries instead of raw wrapper JSON.
- Resume/session restoration UI and persistence improvements for continuing prior work.

### Changed

- Cockpit, model switching, runtime catalog, and session restore flows were tightened around live provider/model state.
- Conversation persistence now records additional session metadata for restore/resume views.

### Fixed

- Tool rendering no longer displays the entire program script or raw `{Output, Stats}` wrapper in the conversation trail.
- Settings, inspector, and launch flows handle updated tool/provider state more consistently.

## [examples/v0.4.0] — 2026-07-12

`examples/v0.4.0`

### Changed

- Example harnesses now handle tool-registration errors explicitly for the stricter lint configuration.
- Example module dependency pin updated for the matching zkit release.

## [zkit/v0.3.1] — 2026-07-10

`zkit/v0.3.1`

### Added

- Typed workflow node IDs, tool-call IDs, and background process IDs to make shared agent/runtime boundaries harder to misuse.
- Validation helpers and constructors for tool effects, LLM response formats, and dynamic MCP connection specs.
- OpenAI/Codex model metadata updates, including GPT-5.6 family defaults and max reasoning effort support.

### Changed

- First-party tool schemas now preserve enum-backed argument types for plan and computer-use tools.
- OpenAI-compatible and Anthropic model discovery now follows paginated provider responses.

### Fixed

- Downstream zarlcode, swebench-eval, and examples call sites now use the stronger zkit ID and enum types.


## [zkit/v0.3.0] — 2026-07-08

`zkit/v0.3.0`

### Added

- Computer-use agent primitives, browser-backed observation/action flows, and computer action/observation tools for agent tool registries.
- Multimodal image media helpers and provider conversion support for image-capable model requests.
- Atomic batch support for anchored hashline edits so related same-file changes can be applied in one verified write.

### Changed

- Model metadata and capability plumbing now carries multimodal and computer-use hints through provider integrations.
- Read-before-write guardrails account for prior successful writes/edits when validating follow-up file edits.
- `golangci-lint` is pinned as a Go tool for reproducible lint checks.

### Fixed

- Browser computer backend cleanup and lint fixes across computer-use tooling.

## [zarlcode/v0.2.0] — 2026-07-08

`zarlcode/v0.2.0`

### Added

- Image attachment support in the TUI composer, transcript flow, and file viewer.
- Browser computer backend wiring for live agent runs.
- Multimodal prompt/context support for providers that accept images.

### Changed

- Editing guidance now prefers cohesive range or batch edits over many tiny adjacent edits.

### Fixed

- TUI layout and live-run plumbing fixes for multimodal/computer-use flows.

## [examples/v0.2.0] — 2026-07-08

`examples/v0.2.0`

### Added

- Computer-use Wikipedia quiz example with browser automation and LLM-generated questions.

### Changed

- Example lint/build flow now uses the repo-pinned lint tool.

## [v0.2.1] — 2026-06-29

`zkit/v0.2.1`

### Added

- Deterministic Go code-understanding helpers: syntax-aware `file_map`, lexical `retrieve_code`, and shared `sourcecode` parsing utilities for callers that need structured source context without embeddings or LSP.
- `models.dev` model metadata integration for context-window and pricing hints.
- Typed tool adapters (`NewTyped`, typed `DecodeArgs`, and effect derivation hooks) so tool implementations can keep typed business logic at the boundary.
- Typed retrieval metadata filters with equality predicates, plus vector-store/qdrant filter plumbing.

### Changed

- Tool argument decoding is now generic and shared across code, dynamic, fetch, search, and zarlcode catalog/instruction tools.
- LLM chat-template kwargs stay typed longer instead of round-tripping through unstructured maps.

### Fixed

- Claude Code inline tool-call leakage is guarded before it reaches the transcript.
- Read-before-write guardrails now treat a prior successful write/edit as established context for follow-up edits to the same file.
- Test and lint cleanup across the CI-covered modules.

## [v0.1.3] — 2026-06-29

`examples/v0.1.3`

### Changed

- Updated example tools to show the latest typed API patterns: `tools.SchemaFor`, generic `tools.DecodeArgs`, typed result structs, and `tools.NewTyped` for new tool adapters.
- Removed hand-written `map[string]any` JSON Schema trees from example tool definitions where the arguments are statically shaped.

## [v0.1.6] — 2026-06-29

`zarlcode/v0.1.6`

### Added

- Lazy instruction-loading tools so agents can discover workspace guidance without eagerly flooding context.
- TUI and headless wiring for the new code-understanding helpers and `models.dev` model metadata.
- Optional local service management for the bundled SearXNG `web_search` Docker Compose service from settings.
- Opt-in Go pprof/runtime metrics and execution tracing flags for profiling zarlcode runs.

### Changed

- Removed the `zarlcode serve` llama-server wrapper; zarlcode now configures model endpoints but leaves local model server lifecycle to Ollama, llama.cpp, LM Studio, or another OpenAI-compatible server.

### Fixed

- Release-dispatch now grants `actions: write`, allowing its follow-up `gh workflow run release.yml` publisher dispatch to succeed under `GITHUB_TOKEN`.

## [v0.2.0] — 2026-06-27

`zkit/v0.2.0`

### Added

- New agent subsystems: `workflow` (graph executor for multi-node flows), `retrieval` (chunking, embedding, and vector-store search), `hitl` (human-in-the-loop review and steering), `checkpoint` (run state store), and `trace` (JSONL event exporter).
- LLM provider rate-limit classification across anthropic, openai, openai-codex, google, and claude-code, surfacing reset/retry timing to the runner.

### Fixed

- Malformed tool-call JSON emitted as text (transposed or missing brackets) is now recovered by a balanced-bracket fast path in `toolparse`, and a runner guardrail re-prompts the model for anything unrecoverable — instead of the call leaking into the transcript as prose.

## [v0.1.5] — 2026-06-27

`zarlcode/v0.1.5`

### Changed

- Bumped the `zkit` dependency to `v0.2.0`, picking up the malformed tool-call recovery and provider rate-limit handling.

### Added

- TUI rate-limit display showing provider reset/retry state.

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
- **File tools** — read, write, edit, grep, glob — workspace-bounded and tracked
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

## [v0.1.4] — 2026-06-25

### Fixed

- `zarlcode upgrade` now ignores and clears legacy local source path configuration, falling back to GitHub release upgrades instead of requiring a source checkout.

## [v0.1.3] — 2026-06-24

### Added

- File viewer image previews for PNG, JPEG, and GIF files.
- Ghostty/Kitty terminal-graphics image rendering when supported, with ANSI block fallback elsewhere.

### Fixed

- Provider startup/runtime errors now surface as user-visible notices instead of silently failing.
- Release-dispatch follow-up verification and zarlcode packaging behavior.

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

[v0.1.4]: https://github.com/zarldev/zarlmono/releases/tag/zarlcode/v0.1.4
[v0.1.3]: https://github.com/zarldev/zarlmono/releases/tag/zarlcode/v0.1.3
[v0.1.2]: https://github.com/zarldev/zarlmono/releases/tag/zarlcode/v0.1.2
[v0.1.1]: https://github.com/zarldev/zarlmono/releases/tag/zarlcode/v0.1.1
[v0.1.0]: https://github.com/zarldev/zarlmono/releases/tag/zarlcode/v0.1.0
