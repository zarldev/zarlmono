# swebench-eval

`sweeval` runs zarlcode against SWE-bench task sets and persists comparable run telemetry in a dedicated SQLite database.

## Build and identify the binary

```bash
go tool task sweeval
nix/bin/sweeval -version
```

Release builds inject the module tag. `go install .../cmd/eval@version` reports its module version; local builds fall back to VCS metadata.

## Run an evaluation

```bash
go run -C swebench-eval ./cmd/eval \
  --tasks /path/to/swebench.jsonl \
  --drivers zarlcode \
  --languages go \
  --sample 5 \
  --task-timeout 10m
```

Every invocation writes one row to `eval_runs` and one row per task/driver to `eval_results` in `~/.zarlcode/swebench-eval.db` by default.

## Verified re-drive

Set `--zarlcode-verified-attempts` above one to evaluate each candidate patch with the official SWE-bench verifier and re-drive rejected attempts:

```bash
go run -C swebench-eval ./cmd/eval \
  --tasks /path/to/tasks.parquet \
  --zarlcode-verified-attempts 3 \
  --zarlcode-verify-workers 2 \
  --score
```

The result row records:

- `verified`: the in-run world-checking goal accepted an attempt;
- `attempts`: attempts consumed by the agent run;
- `attempt_verdicts`: the ordered JSON verifier history.

Historical rows created before migration `00004` have `verified = 0`, `attempts = 0`, and an empty verdict history. That means **verification telemetry was not recorded**, not that a newly verified run necessarily failed.

`--zarlcode-thread-transcript` carries the prior agent thread between attempts. It costs substantially more context, so the default re-drive includes verifier feedback only.

## Database migrations

The eval database is separate from zarlcode's `state.db`. Opening the store applies embedded Goose migrations. Migration `00004` adds verification telemetry using backward-compatible defaults; downgrade removes those columns and their data.

Before experimenting with downgrade or old binaries, copy the database. The eval tool does not own zarlcode's session schema.
