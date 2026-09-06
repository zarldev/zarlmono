# AGENTS.md — `zarlcode/engine`

Owns composition of providers, tools, guardrails, instructions, skills, agents, settings, and live/headless runner lifecycles.

## Composition boundaries

- Construct dependencies from known-valid repository values; validate only external config, stored representations, provider data, and tool arguments at their entry boundary.
- `RuntimeCatalog` snapshots are immutable views. Reload builds a complete replacement before publishing it.
- Root instructions are eager prompt context. Nested instructions remain discoverable through the instruction tools and load only when relevant.
- Agent profile resolution must preserve provider/model inheritance, work mode restrictions, depth limits, and parent workspace confinement.
- Tool registration must retain mutation, workspace-access, scope, approval, and error-kind metadata through wrappers and dynamic sources.

## Lifecycle

- `LiveRunner` owns a turn and the runtime resources it constructs. Borrowed dependencies must be documented and closed by their composition root after the runner drains.
- Shutdown is one-way and idempotent: reject new turns, cancel active work, wait for it, then close owned MCP/browser/fetch/spill resources exactly once.
- A caller deadline bounds that caller's wait; it must not abandon the underlying drain or create a second cleanup owner.
- Background processes are owned by the process manager supplied by the composition root, not by individual turns.
- Never emit logs to stdout or stderr while the alt-screen TUI is active; use the configured file logger and UI events.

## Verification

Exercise focused contracts first, and use the race detector for lifecycle or shared-state changes.

```bash
go test -C zarlcode -count=1 ./engine
go test -C zarlcode -race ./engine
```
