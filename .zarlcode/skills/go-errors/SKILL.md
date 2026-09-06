---
name: go-errors
description: Go error reference for zarlmono covering stable semantic identities, diagnostic causes, wrapping, translation, cancellation, and single-point logging.
---

# Go Error Handling Reference

[go-style](../go-style/SKILL.md) is normative for cross-cutting Go policy. This skill provides detailed error construction and mapping guidance.

## Errors tell a story

Wrap operational failures with concise context and `%w` when cause inspection is intended:

```go
return fmt.Errorf("open config: %w", err)
return fmt.Errorf("parse endpoint: %w", err)
```

Avoid stuttering prefixes such as “failed to”, “unable to”, “could not”, and “error while”. The chain should read as one causal narrative. Do not include credentials, sensitive URLs, or raw private input in diagnostic context.

## Semantic identities and diagnostic causes

Use a small package-owned vocabulary when callers must branch. Define sentinels at package scope and test with `errors.Is`. Use typed errors when callers need structured detail through `errors.As`.

Separate the stable public contract from useful backend diagnostics. `%w`, `Unwrap`, and `errors.Join` make underlying identities reachable by `errors.Is`/`errors.As`; they are not diagnostic-only storage.

When the boundary intentionally permits cause inspection, translation can expose both identities:

```go
if errors.Is(err, storage.ErrNotFound) {
    return fmt.Errorf("%w: %w", ErrUserNotFound, err)
}
```

Document which identities callers may rely on; applications should branch on the domain identity, not an incidental driver cause. If backend identity must remain hidden, wrap only the stable semantic error and retain sanitized diagnostic detail without unwrapping the backend (for example, `%v` for an approved safe cause). A boundary-specific error representation may retain private diagnostics where required. Do not blindly multi-wrap all translated errors, discard actionable causes, or log-and-return just to retain diagnostics.

Never compare error strings or allocate a would-be sentinel inside a function.

## Cancellation

Cancellation semantics belong to the API contract:

- A domain API may translate cancellation to its own `ErrCanceled` sentinel.
- A low-level boundary may preserve `context.Canceled` or `context.DeadlineExceeded` when callers need those identities.
- Observe `ctx.Done()` in cancellable loops and asynchronous work.
- Do not mechanically replace every context error or log expected cancellation.

See [go-concurrency](../go-concurrency/SKILL.md) for ownership and shutdown behavior.

## Logging

Internal packages return errors; they do not log-and-return. Log an unexpected error once at the HTTP, CLI, worker, or process boundary that consumes it. Expected client and domain outcomes are normally mapped without error logging.

## Boundary mapping

Map documented semantic identities with `errors.Is`/`errors.As` to HTTP, ConnectRPC, CLI exit, retry, or worker behavior. Keep implementation-specific errors out of stable public contracts and sensitive diagnostics out of client responses.

When independent work must complete, collect errors in a meaningful deterministic order and use `errors.Join` after draining required work. Joining also exposes each constituent identity; apply the same boundary decision as for wrapping.

## Review checklist

- Does every operational wrap add useful context?
- Can callers branch by documented identity rather than text?
- Are diagnostic causes retained deliberately without accidentally promising driver identities?
- Is cancellation behavior explicit at the boundary?
- Is an unexpected failure logged exactly once?
- Are expected outcomes mapped without noisy logs?
- Are sensitive details excluded from public responses and unsafe logging?
