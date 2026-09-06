# AGENTS.md — `swebench-eval`

SWE-bench evaluation driver in its own module so Parquet and evaluation-only dependencies do not enter the product or shared-library modules.

## Comparison integrity

- Keep driver names, ablation identifiers, report labels, sampling order, and serialized verdict order deterministic.
- Reject unknown configuration, driver, and ablation names. Never silently interpret them as baseline because that corrupts comparisons.
- Preserve the distinction between “not recorded,” “not run,” verifier rejection, infrastructure failure, timeout, and agent failure in storage and reports.
- Historical database defaults are compatibility representations, not evidence that old runs actually produced those values.
- Task filters and sampling operate before concurrent execution so worker scheduling cannot change the selected set.

## Lifecycle and side effects

- Every task, verifier, worker, subprocess, and temporary workspace has a context-bound owner, stop condition, and wait/cleanup path.
- Bound task execution and verifier concurrency independently; one stuck verifier must not leak a worker or block database shutdown.
- Keep task workspaces isolated. Never mutate the source dataset or the user's zarlcode session database.
- Persist enough identity and ordered attempt telemetry to reproduce and compare a result without recording credentials or provider secrets.
- Verified re-drive must pass verifier feedback through the same tool, guardrail, and completion policies as the original attempt.

## Data and migrations

- The evaluation SQLite database is separate from zarlcode's `state.db`.
- Embedded migrations are forward compatibility contracts. Additive migrations require truthful defaults for historical rows; destructive downgrade behavior must be documented.
- Keep transactions small and explicit. Do not hold a write transaction while an agent, verifier, provider, or subprocess runs.

## Verification

Use deterministic fixtures and fake/scripted drivers for package tests. Live provider calls and the official external verifier are explicit integration operations, not unit-test prerequisites.

```bash
go test -C swebench-eval -count=1 ./...
go test -C swebench-eval -race ./...
```
