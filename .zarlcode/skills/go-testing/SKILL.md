---
name: go-testing
description: Go testing reference for zarlmono covering required external-package tests, public behavior, tables, truthful implementations, contracts, cleanup, deterministic time, and race checks.
---

# Go Testing Reference

[go-style](../go-style/SKILL.md) is normative for cross-cutting Go policy. Inspect `go.mod`, `go.work`, and the active toolchain before using version-specific testing APIs.

## Test from the consumer's view

This repository requires black-box tests in external `*_test` packages (for example, `package tools_test`). Do not add same-package tests or new `*_internal_test.go` coverage. Expose only the smallest behavior-specific public seam when necessary and follow owning nested instructions. Existing internal tests are not permission to add more or authorization for unrelated rewrites.

Prefer, in order:

1. Real implementations when practical.
2. Truthful in-memory implementations.
3. Narrow handwritten stubs.

Do not use a mock framework or assert call choreography unless calls themselves are the contract.

## Table-driven tests

Use coherent tables and `t.Run`. Store the expected `err error`, not `wantErr bool`, and test identities with `errors.Is` or structure with `errors.As`.

Use focused scalar checks, `slices.Equal`, or `cmp.Diff` rather than stringifying structures.

## Fixtures and cleanup

Use the active toolchain's testing lifecycle APIs. When supported, prefer:

- `t.Context()` for request-scoped test work;
- immediate `t.Cleanup()` after acquisition;
- `t.TempDir()` for files;
- `t.Setenv()` for process environment.

Use `t.Helper()` in helpers. Use `t.Parallel()` only with fully isolated fixtures. Cleanup must stop and wait for dependents before closing their dependencies; LIFO registration helps only when it matches that order. Use a fresh bounded cleanup context when request/test cancellation has already occurred.

## Contract tests

Run the same observable contract against substitutable implementations with fresh isolated state. Cover the semantics promised by the interface, including:

- not-found and conflict identities;
- writes, reads, ordering, pagination, and external input rejection;
- runtime transition conflicts even with valid typed inputs;
- cancellation;
- independent snapshots and mutable-alias protection;
- atomicity and rollback;
- concurrency guarantees.

Do not assert SQL text, private layout, backend call counts, or other implementation details in shared contracts. Do not test impossible trusted nil wiring as if it were an input contract. Conformance assertions may live in external integration tests when placing them beside implementations would introduce import cycles.

## Time and concurrency

Use `testing/synctest` when supported for timers, deadlines, goroutines, and quiescence. Do not use arbitrary real sleeps. Run `go test -race` for concurrency or shared-state changes. Include lifecycle-owner construction failure and stop/wait paths where relevant.

## Repository verification

Use `go test -C <module> <focused packages>`; root `./...` does not cross modules. Root `go tool task check`, `lint`, and `race` targets provide broader verification when warranted. For documentation-only changes, validate references, frontmatter, and diff consistency instead of claiming runtime suites were exercised.

## Review checklist

- Does the external-package test exercise observable behavior?
- Is the expected error a precise documented identity?
- Are fixtures isolated and cleaned immediately in dependency-safe order?
- Does each substitutable implementation run the same contract?
- Are aliasing, atomicity, cancellation, and concurrency tested where promised?
- Is time deterministic for the active toolchain?
