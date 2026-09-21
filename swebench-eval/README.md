# swebench-eval

`sweeval` runs zarlcode against SWE-bench task sets and persists comparable run telemetry in a dedicated SQLite database.

## Build and identify the binary

```bash
go tool task sweeval
~/.local/bin/sweeval -version
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

Tool scheduling defaults to up to four concurrent workspace reads, with ordered
barriers around writes, shell commands, and unclassified tools. Use
`--tool-concurrency 1` for sequential execution; `0` selects the automatic default,
and values above one explicitly parallelize all tool calls up to that limit.
When comparing runs from older builds, account for this changed default: older
builds treated `0` as sequential. The manifest records the requested value and
build identity.

## Run identity and offline export

New evaluations capture a versioned manifest in the run row before any task is
materialized or any provider is invoked. It records:

- The selected task definitions in execution-input order, including repository,
  base commit, problem statement, test metadata, and patches from the dataset.
- A SHA-256 fingerprint of the ordered tasks, and the expanded driver names
  (`--ablations judge,baseline` records `zarlcode-judge` and `zarlcode`).
- Requested model and runtime settings, including timeouts, concurrency, context
  window, verification policy, and scoring configuration.
- Available executable build identity: version, full VCS revision and dirty
  status, Go version, platform, and dependency versions/replacements.

Export an existing run without loading tasks, constructing providers, cloning
repositories, or invoking the scorer:

```bash
go run -C swebench-eval ./cmd/eval \
  --db /path/to/eval.db --export-run RUN_ID > run.json
```

The artifact contains `format_version`, `run`, `manifest`, `results`, and
`score_attempts`. All records are read in one database transaction. Results are
ordered by task ID and driver; scoring events retain insertion order. Exports of
an unchanged run are deterministic. A run still in progress retains its missing
end time and only the results committed at the snapshot boundary.

The manifest records **requested** settings. Zero/default values are preserved;
the provider and model actually used remain per-result fields. Environment
contents, endpoint URLs, credentials, and local configuration paths are excluded
from manifest capture; presence flags identify omitted inputs such as an env file
or reset URL. Those external settings, evaluator installation, local model/server
state, and uncommitted/local dependency source must be retained separately to
reproduce a run. A dirty flag or local replacement marker does not identify the
changed bytes. Build metadata unavailable to the executable remains absent.

Historical runs export `manifest: null`. Unscored results keep `resolved: null`,
distinct from `false`. All-zero legacy token counts export `usage: null` because
the database cannot distinguish missing usage from reported zero. Nonzero totals
are exported as recorded; no dollar cost is inferred without a recorded pricing
basis. The artifact includes task text, patches, notes, and existing error
diagnostics, so choose what to share accordingly.

Before comparing runs, check the task fingerprint and per-result identities,
account for missing/error results, and compare the settings that were meant to
stay fixed. Export provides the evidence for comparison; it does not declare two
runs equivalent or fill in outcomes for unfinished tasks.

The export path opens the existing evaluation database through the normal
migration flow. A missing database or run is an error. A malformed saved JSON
field rejects the export before writing an artifact.

For an offline contract check with deterministic local failures and no provider:

```bash
go test -C swebench-eval -count=1 ./cmd/eval \
  -run TestCLIRecordsSelectedInputsAndExportsWithoutExecution
```

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

Migration `00007` adds the run manifest with an empty historical default meaning
"not recorded". Downgrading drops the captured inputs and build metadata.
