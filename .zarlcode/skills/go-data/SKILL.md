---
name: go-data
description: Database and repository reference for zarlmono covering SQLite and SQLC, PostgreSQL, document stores, migrations, semantic errors, transactions, memory contracts, and ownership.
---

# Database and Repository Reference

[go-style](../go-style/SKILL.md) is normative for domain ownership, interfaces, errors, lifecycle, and tests. This skill provides database-specific implementation guidance.

## Repository contract

Apply [go-style](../go-style/SKILL.md) for consumer capabilities, acyclic conformance, representation boundaries, and legitimate reusable library APIs. [go-errors](../go-errors/SKILL.md) owns diagnostic cause exposure versus stable semantic identities such as `ErrNotFound`, `ErrConflict`, and `ErrConstraint`.

## In-memory implementation

Provide a truthful memory implementation when the repository contract permits it. It must:

- use the same observable error identities;
- honor cancellation promised by the interface;
- protect complete atomic transitions under one owner lock;
- clone mutable inputs and outputs when independent snapshots are promised;
- avoid retaining caller-owned mutable aliases unless the contract explicitly permits borrowing; a real transfer requires callers to relinquish mutable aliases;
- support the same contract tests as persistent adapters.

Inspect current `github.com/zarldev/zarlmono/zkit/zsync` APIs when a synchronized shared collection fits; do not bypass domain atomicity merely to use it. `Map.Get` and `Set` individually synchronize entries, not a compound read-check-write transition or nested mutable values.

## Current zarlmono SQLite persistence

`zkit/db` is the zarlcode state persistence layer, not a universal SQL connection factory. `Open(ctx, path) (*Store, error)` opens SQLite and applies embedded Goose migrations; `Store.Close() error` releases its owned connection. Consumers normally use typed state methods, not the exposed `DB()` escape hatch.

Its `sqlc.yaml` uses SQLite with `queries/`, `migrations/`, and committed `gen/` output. Follow [sqlc reference](references/sqlc.md) for the root-pinned generator. Do not manually change generated code or introduce a second query tool into this adapter.

- Enable required pragmas explicitly, including foreign keys where the schema depends on them.
- Choose connection limits and journal mode from the driver and deployment concurrency model; do not assume one global setting fits every application.
- Follow existing migration and query tooling in each adapter; adopting SQLC elsewhere is a scoped repository decision, not a universal library mandate.
- Test locking, transaction, and persistence behavior promised by the repository.

## PostgreSQL when applicable

- In an adapter that adopts SQLC, use it for fixed-shape queries; see [sqlc reference](references/sqlc.md).
- Open pools in application composition and pass them as borrowed dependencies.
- Translate `pgx.ErrNoRows` to the repository's not-found identity.
- Translate constraint codes to semantic identities; retain safe diagnostic causes according to the boundary's exposure contract.
- Keep connection configuration, SQLC types, and nullable wrappers inside the adapter.

These are PostgreSQL adapter examples, not a claim that `zkit/db` exposes a pgx pool.

## MongoDB and document stores

Inspect `github.com/zarldev/zarlmono/zkit/docstore` before use. It provides concrete `NewMemoryStore[T Value[T]]()` and `NewMongoStore[T Value[T]](collection)` constructors, not a universal `DocumentStore` interface. Consumers define narrow capabilities. `Value[T]` requires `Clone() T`; implementations must make genuinely independent copies of nested mutable state to satisfy the snapshot contract.

Keep document representation and identifiers inside application adapters where they differ from domain values. Translate package and driver errors into the domain repository vocabulary. A Mongo store receiving a collection borrows it; the database/client owner manages lifecycle.

## Migrations

Use repository-configured migration tooling. Migrations must be ordered, reproducible, reversible when safely possible, and reviewed with the application rollout that consumes them.

For Goose SQL migrations:

```sql
-- +goose Up
CREATE TABLE users (
    id UUID PRIMARY KEY,
    email TEXT NOT NULL UNIQUE
);

-- +goose Down
DROP TABLE users;
```

Adapt schema types to the actual backend. Do not assume destructive down migrations are operationally safe; follow repository deployment policy.

## Transactions

Put transaction scope where the invariant is known. Begin, register rollback immediately, use transaction-bound queries, and commit only after all invariant steps succeed.

Do not expose `*sql.Tx`, `pgx.Tx`, generated query objects, or generic transaction callbacks through application domain interfaces. Low-level reusable transaction capabilities may legitimately expose such mechanics as their own contract.

Memory repositories must lock the same complete operation that SQL performs atomically. Contract tests should verify rollback and no partial visibility.

Resource openers own cleanup unless ownership is explicitly transferred. Stop and wait for dependent work before closing connections; reverse acquisition order is useful only when it matches dependency order.

## Contract verification

Use the shared observable contract matrix in [go-testing](../go-testing/SKILL.md) for every substitutable implementation with fresh state. Database-specific coverage includes transaction rollback, no partial visibility, backend locking, and persistence guarantees.

## Review checklist

- Are driver and generated types contained at application boundaries?
- Are stable semantic errors distinct from incidental backend causes?
- Who opens and closes every connection or client?
- Does transaction scope match the invariant?
- Does memory behavior match persistent behavior?
- Can mutable state alias across the repository boundary?

## On-demand SQLC mechanics

Load [SQLC configuration, annotations, transactions, and generation](references/sqlc.md) only when changing a SQLC-owned adapter.
