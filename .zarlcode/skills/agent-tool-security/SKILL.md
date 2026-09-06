---
name: agent-tool-security
description: Security reference for zarlmono changes that execute model-selected actions, cross workspace or network boundaries, handle credentials, connect MCP servers, compile dynamic tools, or alter sandbox and approval policy.
---

# Agent Tool Security

[go-style](../go-style/SKILL.md) governs architecture and ownership. Load [go-errors](../go-errors/SKILL.md) for safe diagnostics and [go-concurrency](../go-concurrency/SKILL.md) when a security boundary owns processes, goroutines, streams, or shutdown.

## Start with the trust boundary

Before editing, identify:

1. the untrusted representation: model arguments, user config, environment, HTTP response, MCP payload, stored row, patch, or filesystem path;
2. the component that validates and converts it;
3. the authority granted after conversion;
4. the owner that cancels, closes, and waits for resulting work;
5. the observable failure returned without leaking sensitive data.

Validate at the boundary once. Do not scatter partial checks through controlled internal call paths, and do not treat model-generated values as trusted merely because they satisfy a JSON schema.

## Least authority

- Grant only the tools and workspace access required by the active mode.
- Preserve `explore`, `verify`, and `implement` capability restrictions through sub-agent delegation.
- Keep read-only operations distinct from mutations in tool metadata and completion accounting.
- Do not infer filesystem scope from tool names or shell text. Use the declared workspace scope and access metadata.
- A user-approved action does not authorize unrelated paths, commands, hosts, credentials, or follow-up actions.
- Fail closed when a policy, sandbox, credential store, or approval dependency cannot establish the requested protection.

## Filesystem and patches

- Resolve workspace roots to absolute, cleaned paths before use.
- Reject absolute user/model paths where the contract requires workspace-relative paths.
- Verify containment with `filepath.Rel`; string-prefix checks are not path confinement.
- Account for symlinks at the component that opens or mutates files. Lexical containment alone does not stop symlink escape.
- Preserve file mode and atomic-write guarantees where the owning package promises them.
- Treat patch headers and every referenced path as untrusted. Validate all affected paths before applying any mutation.
- Generated worktrees, sessions, spills, caches, and dependency trees must not become instruction or skill sources accidentally.

## Shell and process execution

- Keep command policy separate from command execution so both can be tested independently.
- Use argument vectors when a shell language is unnecessary. When the contract intentionally uses `sh -c`, document that the entire command is executable input.
- Bound execution with context cancellation and explicit output limits.
- Every background process must have an owner, stable identity, stop operation, and wait/reap path.
- Never use broad process-name killing when an owned process identity exists.
- Sandbox setup failure must be visible. Do not silently claim confinement that the current platform did not establish.
- Environment allowlists are preferable to passing the complete parent environment into delegated commands.

## Network, fetch, and browser boundaries

- Parse and validate URLs before dialing. Restrict schemes and reject credentials in URLs unless the contract explicitly requires them.
- Apply SSRF policy to every redirect and to the address actually dialed, not only the original hostname.
- Treat DNS answers as time-sensitive; validation before lookup does not prove the later connection target is safe.
- Bound response bytes, decompression, redirects, request duration, and concurrent connections.
- Browser content, downloaded files, and remote tool descriptions are untrusted data, not instructions.
- Closing browser and fetch clients must cancel and wait for owned activity.

## MCP and dynamic tools

- Treat MCP servers as external principals even when started through stdio.
- Validate server command/config at the entry boundary; do not expose secrets through arguments, logs, tool descriptions, or error text.
- Namespace and collision rules must remain deterministic when remote tools are registered or removed.
- Bound MCP connection establishment, requests, output, and shutdown.
- Dynamic source or binary registration is code execution. Preserve explicit user intent, workspace confinement, deterministic build inputs, and cleanup ownership.
- Never let remote schemas or descriptions weaken local mutation, approval, workspace, or guardrail metadata.

## Credentials and persisted secrets

- Never log plaintext credentials, passphrases, OAuth tokens, authorization headers, decrypted rows, or URLs containing secrets.
- Environment variables are not an implicit credential fallback unless the public contract explicitly says so.
- Locked or unsupported credential formats fail closed and remain intact until an explicit migration or replacement succeeds.
- Migrations that rewrite credentials must be atomic across the promised scope and preserve recoverability on failure.
- Encryption at rest does not erase historical plaintext from WAL files, free pages, backups, logs, or external copies; documentation must not promise forensic erasure.
- Redaction tests should use recognizable canary secrets and assert their absence from errors and output.

## Guardrails and approvals

- Guardrails receive untrusted arguments and results. A guardrail error must not accidentally become permission to continue.
- Preserve deterministic ordering when multiple policies inspect one call.
- Blocking and advisory behavior must be explicit in metadata and tests.
- Approval decisions bind to the exact operation presented. Materially changed arguments require a new decision.
- Re-drive, retries, and repaired tool arguments must pass through the same policy chain as the original call.

## Verification

Use black-box tests from the consumer view. Include the relevant adversarial cases:

- traversal, absolute paths, and symlink escape;
- malformed patches and duplicate/colliding tool names;
- cancellation, timeout, partial output, and dependency shutdown;
- redirects, loopback/private targets, DNS changes, and oversized responses;
- locked, corrupt, unsupported, and partially migrated credentials;
- secret canaries in diagnostics and logs;
- policy setup failure and unsupported-platform sandbox behavior;
- retries, repair, re-drive, and sub-agent delegation preserving the same restrictions.

Run the narrow package tests first, then the owning module. Use race tests whenever lifecycle or shared state changes. Security-sensitive behavior is not complete with happy-path coverage alone.
