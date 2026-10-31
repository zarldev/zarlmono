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

### Evaluation test-file policy

The evaluator now explains its active strict test-file policy in the default
system prompt: existing tests may be read and run, but test files/fixtures must
not be created or edited, including through another tool. Changes should target
production source. This is evaluation-only guidance; the shared canonical prompt
and interactive agent are unchanged, and enforcement is not weakened.

The appendix follows the **composed guardrail chain**, not an ablation label.
`--ablations no-test-edit` therefore removes both strict test-edit enforcement and
its explanatory appendix. Treat that arm as removal of the policy and its guidance,
not as an enforcement-only comparison with identical prompts.

New run manifests record `evaluation_prompt_revision: "test-file-policy-v1"`.
This identifies prompt-composition behavior, not whether every ablation enables
test protection. Historical manifests without that field remain unchanged and
must not be assumed to contain the new guidance. Experimental baseline trials now
inherit this default; replaying the earlier `source-only` candidate would duplicate
its substance rather than reproduce the original baseline-versus-candidate study.

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

## Prompt-guidance experiments (opt-in)

This is a controlled comparison facility, not an automatic harness optimizer.
It changes only an attributed prompt appendix. It does not load the interactive
skill/subagent catalogue, edit permissions, or promote guidance into live sessions.

Create `experiment.json`:

```json
{
  "version": 1,
  "repeats": 2,
  "candidates": [
    {"label": "navigation", "guidance": "Read the owning package and its callers before editing."}
  ]
}
```

The unchanged baseline is automatic. This example runs four trials per task:
`zarlcode-harness-baseline-r1`, `zarlcode-harness-navigation-r1`, then their `r2`
repetitions. Each gets an independent checkout at the same base commit. The
existing console report and JSON run export distinguish those driver labels.
Ordering is baseline-first within each repetition; this initial implementation
does not randomize trial order or declare a statistically significant winner.

A **live run incurs provider costs**. After selecting a small fixed task set,
model, budget, and appropriate network restrictions:

```bash
go run -C swebench-eval ./cmd/eval \
  --tasks /path/to/tasks.jsonl --sample 2 \
  --harness-experiment /path/to/experiment.json \
  --harness-evidence-dir /path/to/private-evidence \
  --zarlcode-provider YOUR_PROVIDER --zarlcode-model YOUR_MODEL \
  --task-timeout 5m --max-iter 20
```

Experiments require explicit provider/model and evidence directory, reject
ablations and verified re-drive, and keep the normal guardrail chain. `--score`
may score patches **after** all trials finish; scoring never feeds back into
these trials. Count repeats and candidates when budgeting: total solver runs
are `selected tasks × repeats × (1 + candidates)`. No proposer/composer calls
are made. Compare against repeated unchanged-harness runs before attributing
any improvement to guidance.

Input is snapshotted once, before execution: version 1, 1–10 repeats, 1–16
candidates, at most 16 KiB guidance each, and at most 256 KiB JSON. Unknown fields
and invalid/duplicate labels are errors; `baseline` is reserved. Normal runs
retain manifest version 1; experiments use version 2, recording exact decoded
guidance, SHA-256 digests, expanded trials, and the evidence directory basename.
Re-running allocates a new artifact directory; no experiment resume is implied.

The printed private `experiment-*` directory contains:

- `experiment.json`: run ID and immutable trial snapshots, without dataset answers.
- `trial-*.jsonl`: `start`, `history`, `request`, and terminal `result` records,
  joined to database results by run/task/driver identity rather than filenames.
  History uses the runner's byte-preserving replay format and survives compaction;
  request records preserve the model-facing prompt/tool surface but omit opaque
  provider options. Decode history/request data using `runner.UnmarshalReplayMessage`
  and `runner.UnmarshalHistoryRequest`, respectively.
  Each history record's data is an array of replay occurrences. `task_prompt`,
  result `diff`, and result `error` use `{ "text": "..." }` for UTF-8, or
  `{ "bytes_base64": "..." }` for arbitrary bytes; an empty object means empty text.

Each trial artifact is limited to 64 MiB and synced/closed at settlement. Capture
failure stops further model requests and marks the trial as an error. Missing
artifacts (for example, checkout failure) or a missing terminal result mean
incomplete evidence, never success. Consult the database error as well: a late
sync failure may leave a terminal record in an unusable artifact. Zero token
counts retain the existing driver's unknown-or-zero ambiguity; no cost is inferred.
The 64 MiB cap bounds stored bytes, not transient JSON-encoding memory.

Artifacts deliberately omit grading lists, official verdicts, provider options,
and raw subprocess logs. **They are not automatically sanitized model input:**
problem text, guidance, tool output, patches, and diagnostics can contain secrets
or retrieved answers. Inspect/redact before sharing or supplying to an improver.
The normal run export still contains gold/test patches and must not be used as
improver context. Private files use mode `0600`; newly allocated directories use
`0700`. Protect any pre-existing parent directory yourself.

Fresh checkouts prevent edit interference, not answer leakage. The tool does not
install network restrictions or hide repository history. Establish those controls
outside the experiment before making benchmark claims. Active skills, instructions,
profiles, and preferences are never updated by this facility.

Offline verification (no credentials, provider calls, or official scorer):

```bash
go test -C swebench-eval -count=1 ./harness ./evalconfig ./cmd/eval ./runner
```

## One-round automatic refinement (opt-in)

`--harness-refine` runs a single **Propose–Probe–Compose** round above the normal
evaluator. It is an experimental controller, not a self-modifying live agent:

1. Run the unchanged evaluation baseline.
2. Ask a tool-less improver for `general` and `domain` guidance, or no change.
3. Run both candidates in fresh checkouts of the original task commit.
4. Ask the improver to compose guidance from the three execution projections,
   or declare no change.
5. Run the composed guidance in another fresh checkout, then freeze the round.

The maximum is **four solver rollouts and two application-level improver calls
per task**. No-change decisions skip later stages explicitly. A stage/capture
failure stops the round; it is never silently converted into success. There is
no automatic promotion, cross-task memory, resume, recursive retry, or new tool
permission. Optional `--score` runs only after every round has frozen; verifier
re-drive is prohibited. A composed harness is not automatically declared better.

Example **live, paid** invocation after preparing an isolated credential/provider
store and appropriate filesystem/network controls:

```bash
go run -C swebench-eval ./cmd/eval \
  --tasks /path/to/tasks.jsonl --sample 1 \
  --harness-refine --harness-evidence-dir /private/evidence \
  --state-db /private/evaluation-state.db \
  --zarlcode-provider YOUR_PROVIDER --zarlcode-model YOUR_MODEL \
  --max-iter 8 --task-timeout 90s --harness-improver-timeout 60s
```

The initial mode requires `--sample 1..10`, task concurrency 1, explicit provider
and model (shared by solver and improver), 1–64 solver iterations, a positive task
timeout no greater than 10 minutes, and an improver timeout no greater than two
minutes. It rejects static `--harness-experiment`, ablations, verified re-drive,
transcript threading, and environment-file loading. Existing non-refinement modes
are unchanged. Total round time is bounded by four task timeouts plus two improver
timeouts, subject to underlying operations honoring cancellation.

The improver advertises **no tools** and rejects surfaced tool calls. Its input
is capped at 64 KiB including instruction text, its retained response at 16 KiB,
and its requested output limit is 2,048 tokens. Token limits depend on provider
support and are not a hard billing guarantee; provider transport retries may occur
inside a bounded call. Responses must be strict JSON with exact stage labels,
one repetition, a bounded rationale, and valid citations to supplied evidence IDs.
Malformed/oversized responses stop the round; there is no automatic repair call.

### Evidence and provenance boundaries

The model-facing `metadata-v1` projection contains the original task prompt,
current candidate guidance, prompt revision, ordered tool names/status/kinds,
terminal reason, and reported token totals. It excludes raw tool arguments/output,
error prose, patches, prepared solver requests, private reasoning, continuation
items, grading lists, and official verdicts. This is deliberately less informative
than a full behavioral transcript. Large projections fail explicitly rather than
being silently truncated. Zero solver token totals retain the driver's existing
unknown-or-zero ambiguity.

Each probe returns a receipt containing its artifact basename, identity and
SHA-256 digest after successful capture settlement. The reader confines resolution
with `os.Root`, verifies digest/identity/revision, requires a terminal record, and
accepts at most 256 unique execution observations. Cancelled/error trials are not
improver evidence. Failed stages preserve any available receipt; an
`incomplete_artifact` entry is only an unverified filename, not completed evidence.

Manifest version 3 records the **planned maximum** driver topology, limits, and
projection profile. Candidate bytes do not exist at initial manifest capture:
they are retained in private `round-*.jsonl` journals under the evidence namespace.
Journals are synced around each external stage and capped at 1 MiB per task. They
contain exact projected inputs, validated decisions, base64-encoded raw response
bytes, optional reported improver usage, receipts, failures, and explicitly skipped
drivers. No terminal `frozen` record means the round did not finish. Existing
console/JSON result reports cover solver rows; include improver usage from journals
when comparing total inference cost. Align executed task IDs across arms before
comparing scores; a skipped arm is not a failed or zero-scored trial.

Task prompts and generated guidance are still untrusted text. Metadata-only
projection is **not** general secret sanitization, and an improver citation does
not establish that its claim is correct. The solver retains its original tool
permissions: no extra network isolation or benchmark-answer blocking is installed.
Use dedicated environments, keep credential/artifact stores outside solver-readable
grants (not merely outside the checkout), and restrict egress before live studies.
An explicit `--state-db` is required and aliases of the default session database
are rejected. The mode does not copy, unlock, or migrate that default database;
operators must supply genuinely isolated evaluation credential state.

Offline verification exercises a local scripted HTTP provider, all four fresh
checkouts, both improver calls, receipt/citation limits, no-change/failure paths,
secret/grading canaries, and cancellation without paid model calls:

```bash
go test -C swebench-eval -count=1 ./...
go test -C swebench-eval -race -count=1 ./...
```

## Database migrations

The eval database is separate from zarlcode's `state.db`. Opening the store applies embedded Goose migrations. Migration `00004` adds verification telemetry using backward-compatible defaults; downgrade removes those columns and their data.

Before experimenting with downgrade or old binaries, copy the database. The eval tool does not own zarlcode's session schema.

Migration `00007` adds the run manifest with an empty historical default meaning
"not recorded". Downgrading drops the captured inputs and build metadata.
