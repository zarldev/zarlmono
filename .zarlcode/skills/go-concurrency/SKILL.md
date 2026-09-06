---
name: go-concurrency
description: Go concurrency reference for zarlmono covering goroutine ownership, cancellation, channels, atomic transitions, bounded workers, dependency-safe shutdown, and race verification.
---

# Go Concurrency Reference

[go-style](../go-style/SKILL.md) is normative for cross-cutting Go policy. Inspect the repository's `go.mod`, `go.work`, and active toolchain before choosing version-specific APIs.

## Lifecycle

Every goroutine has:

1. a starter and owner;
2. a stop condition;
3. a completion and wait path;
4. an error path;
5. explicit data ownership;
6. a defined shutdown order.

Do not fire-and-forget. Prefer explicit startup. Constructors may start work only when the returned lifecycle owner exposes reliable shutdown and waiting, with error delivery. If construction fails before returning the owner, stop and wait for already-started work and clean up acquired resources. Options never start goroutines. See [construction reference](../go-style/references/construction.md) for construction examples.

## Context

Propagate caller contexts. Do not store request contexts in structs or detach middle-layer work with `context.Background()`. An explicit lifecycle owner may retain its own cancellation state; application shutdown may use a fresh bounded cleanup context rather than an already-canceled request context.

Select on `ctx.Done()` in cancellable loops and blocking operations. Whether the public API returns a domain sentinel or preserves a context error is an explicit boundary contract; see [go-errors](../go-errors/SKILL.md).

## Channels and shutdown

- The sending side coordinates channel closure; with multiple producers, a coordinator closes after all sends finish.
- Prefer value elements, with explicit borrowing/transfer/copy semantics for mutable aliases.
- Use unbuffered channels or size one by default.
- Larger buffers require a named bounded-queue or backpressure reason.
- Stop producers, coordinate draining, and wait for users before closing dependencies. Do not wait for a blocked producer while preventing its consumer from draining.
- Never send on or close a channel from multiple uncoordinated owners.
- Cleanup follows dependencies: stop and wait for dependents first. Reverse acquisition/registration order is a default only when it represents those dependencies.

## Synchronization

Keep mutexes private and never embed them. Put an entire invariant-preserving transition under one owner lock; avoid check-then-act races. Trusted inputs do not eliminate checks against mutable state.

Use `RWMutex` only when workload evidence supports it. Do not copy used synchronization primitives. A lock does not protect mutable state after a reference escapes. A synchronized zkit collection does not automatically make a multi-step domain operation atomic.

Use the active toolchain's idiom for wait groups. `sync.WaitGroup.Go` is appropriate only when supported by the repository's Go version.

## Worker pools

Bound workers and queues independently only when they constrain different resources. Do not wrap a bounded worker pool in a semaphore enforcing the same limit.

With `errgroup`, ensure all launched work observes cancellation and that all goroutines are waited before owned resources close.

## Testing

- Run focused external-package tests with `-race` for shared-state or goroutine changes.
- Use `testing/synctest` when supported for time, deadlines, goroutine blocking, and quiescence.
- Avoid arbitrary sleeps and leaked test goroutines.
- Exercise cancellation, shutdown ordering, blocked send/receive, partial failure, and repeated close/stop behavior promised by the API.

## Review checklist

- Who owns and waits for every goroutine, including constructor failure paths?
- Can every blocking operation stop?
- Who closes each channel?
- Is every transition atomic at its owner?
- Can mutable state escape after unlock?
- Are worker and queue bounds justified?
- Does shutdown close dependencies only after their users stop?
