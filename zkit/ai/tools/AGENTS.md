# AGENTS.md — `zkit/ai/tools`

Owns canonical tool contracts, typed schemas, execution, workspace coordination, MCP wrapping, and shared tool error semantics.

## Tool contracts

- Prefer `NewTyped` or `SchemaFor` plus typed decoding. Model arguments remain untrusted after schema generation and are validated at dispatch.
- Preserve `Mutates`, `WorkspaceAccess`, and `WorkspaceScope` through registries, wrappers, fallbacks, dynamic tools, and MCP bridges.
- Use argument-derived scope only for the declared trusted path field or patch format. Opaque and external tools conservatively coordinate at workspace-root scope.
- Tool names, ordering, collision behavior, and call signatures are deterministic compatibility contracts.
- Return typed tool errors with the correct validation, not-found, permission, budget, or fatal kind; downstream policy branches on that kind.
- Tool output is untrusted, size-bounded data. Redaction must occur before content reaches logs, transcripts, or model-visible diagnostics.

## Execution and security

- Workspace containment must account for cleaned paths and symlinks at the file-opening boundary.
- Shell, browser, fetch, MCP, and dynamic-code tools require explicit cancellation, output bounds, and owned shutdown.
- Remote tool descriptions and schemas cannot weaken local mutation, scope, approval, or guardrail metadata.
- Coordinated waits are cancellable and preserve FIFO fairness for overlapping scopes.
- Load `agent-tool-security` for changes to execution authority, confinement, network access, dynamic code, credentials, or policy ordering.

## Testing

Use external-package tests. Cover schema rejection, metadata preservation, cancellation, collisions, redaction, and concurrent overlapping/disjoint scopes.

```bash
go test -C zkit -count=1 ./ai/tools/...
go test -C zkit -race ./ai/tools/...
```
