# SQLC Reference

[go-style](../../go-style/SKILL.md) is normative; [go-data](../SKILL.md) supplies repository contract and ownership detail. This reference covers SQLC-specific mechanics, not a universal requirement for every Go persistence API.

## Scope and current configuration

Use SQLC for fixed-shape queries in adapters that have adopted it. Do not hand-write ordinary bindings/scanning in those adapters. Genuinely dynamic predicates/projections and generic library database capabilities may require other approaches; follow the owning repository decision rather than adding tools mechanically.

The current `zkit/db/sqlc.yaml` configures SQLite:

```yaml
version: "2"
sql:
  - engine: "sqlite"
    queries: "queries/"
    schema: "migrations/"
    gen:
      go:
        package: "gen"
        out: "gen"
        emit_empty_slices: true
        emit_json_tags: false
        emit_interface: false
        emit_exact_table_names: false
        emit_prepared_queries: false
```

Root `go.mod` pins `github.com/sqlc-dev/sqlc/cmd/sqlc` (currently v1.31.1). Reinspect configuration and pin before generation. Do not replace this SQLite setup with an illustrative PostgreSQL/pgx configuration. For a separately approved PostgreSQL adapter, select engine and `sql_package: pgx/v5` from its actual driver.

Select nullable and pointer generation from the repository's domain mapping needs. Generated types never become domain types merely because their Go shape is convenient.

## Query annotations

Use the annotation matching observable cardinality. For example, in a PostgreSQL adapter (adapt placeholders and schema to the actual backend):

```sql
-- name: User :one
SELECT id, email, created_at
FROM users
WHERE id = $1;

-- name: Users :many
SELECT id, email, created_at
FROM users
ORDER BY created_at, id
LIMIT $1 OFFSET $2;

-- name: DeleteUser :execrows
DELETE FROM users WHERE id = $1;
```

Use named `sqlc.arg(...)` parameters when they make generated parameter structs clearer. Select explicit columns; avoid accidental API changes caused by `SELECT *`.

## Generated boundary

- Never edit generated files.
- Keep generated packages logically inside the adapter; current `zkit/db/gen` is not an `internal/` path, but application domain APIs still should not expose its rows.
- Convert generated rows and nullable values explicitly to and from domain values.
- Translate no-row and constraint failures to repository semantic identities. Retain useful diagnostic causes deliberately; wrapping backend causes also exposes their identities. See [go-errors](../../go-errors/SKILL.md).
- Do not enable generated interfaces merely to create an abstraction; consumer interfaces remain with consumers.

## Transactions

Begin transactions through the active driver, register rollback immediately, and use SQLC's generated `WithTx` support.

```go
tx, err := db.BeginTx(ctx, nil)
if err != nil {
    return fmt.Errorf("begin transaction: %w", err)
}
defer tx.Rollback()

qtx := queries.WithTx(tx)
// Use qtx for all steps required by the invariant.

if err := tx.Commit(); err != nil {
    return fmt.Errorf("commit transaction: %w", err)
}
```

This is an illustrative `database/sql` fragment, not a complete program. Adapt exact calls and rollback error handling to the active driver (pgx requires context) and owning operation. Keep transaction scope where the invariant is known; do not leak generic transaction callbacks through application domain interfaces.

## Verification

From the zarlmono repository root, use the pinned tool and actual config:

```bash
go tool sqlc compile -f zkit/db/sqlc.yaml
go tool sqlc generate -f zkit/db/sqlc.yaml
git diff -- zkit/db/gen
go test -C zkit ./db/...
```

When checking reproducibility from an already-generated baseline, `git diff --exit-code -- zkit/db/gen` should be clean. Intentional query changes normally produce generated diffs; review them rather than treating every diff as failure. Do not regenerate for unrelated documentation changes.

## Review checklist

- Is the query shape fixed and owned by a SQLC adapter?
- Are selected columns and ordering explicit?
- Are generated files isolated and untouched by hand?
- Are generated values mapped at the adapter boundary?
- Are semantic errors preserved without an accidental driver API promise?
- Does transaction scope cover the full invariant?
- Was generation run with the repository-pinned version and correct configuration?
