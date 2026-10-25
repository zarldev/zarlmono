# Performance and tool-efficiency comparisons

## Read the measurements correctly

The session dashboard's **performance** section describes the current or last
root task, excluding delegated tasks. `UI.PerformanceDiagnostics()` exposes an
owned transient snapshot to integrations on the UI event owner; it is not part
of persisted spend accounting.

- **Provider duration** sums observed attempts, including retries and failures.
  **Callbacks** are included in that duration, not an additional phase.
- **First output** belongs to the latest attempt and includes content, thinking
  or tool-call output. `none` differs from a measured `0s`. It is not necessarily
  the first visible answer token.
- **Preparation/tools** sum completed-iteration observations. Failed preparation,
  compaction, backoff and final settlement can remain outside those figures.
  Tool duration is batch wall time, not a sum of overlapping tool durations.
- **Reported end-to-end tok/s** includes tool work and waiting. Missing provider
  usage can make it partial. It is not server decoding throughput.
- **Attempt cache** is from that attempt's usage, never stale occupancy from an
  earlier request. The **tool surface** belongs to the latest completed
  iteration, independently of the latest attempt. Fingerprint and change status
  are available in the snapshot. **Current mode** is not exact request-mode
  attribution. **Compactions** count observations during this root task.

These neighboring facts help investigate cache drops and latency; they do not
prove that a mode change, tool change or compaction caused either. Existing
`UI.CacheDiagnostics()` remains a session/physical-target usage rollup, not a
per-request latency trace. Snapshot durations use Go `time.Duration` (nanoseconds
when JSON encoded without a custom codec).

## Compact program output

Batch independent discovery reads with `call_many`. Its worker count bounds
simultaneous execution; a larger batch runs in waves within the invocation's
remaining call budget. Failure to authorize any entry rejects the entire batch
before dispatch. Model result order is declaration order. Mutations and shell
remain direct; this does not grant them parallel or nested authority.

Nested `data` is normalized structured tool data, **not** the direct tool's
`String()` rendering. For example, grep data has `Hits` and `Truncated`, while
individual hits have `file`, `line` and `text`. Changing `output` does not change
that nested shape.

```python
r = call("read", {"path": "main.go", "offset": 0, "limit": 80})
emit("main.go\n" + r["data"] if r["ok"] else r)
```

```python
r = call("grep", {"pattern": "TODO", "max_results": 20})
emit({"hits": r["data"]["Hits"], "truncated": r["data"]["Truncated"]} if r["ok"] else r)
```

Preserve errors and partial results, source paths, edit anchors, applicable
guidance and truncation/recovery notices. For multiple reads, emit labelled text
rather than re-encoding every input/envelope. Transforming an output does not
establish delivery of an unchanged child result; existing admission accounting
still applies. Tool signatures include enum choices, explicit schema defaults,
and read-offset semantics without duplicating full schemas.

A provider-free local comparison exercises both output shapes:

```sh
go test -C zkit ./agent/tools/program -run '^$' \
  -bench BenchmarkProgramOutputProjection -benchmem -count=3
```

Compare `output-bytes/op` first. CPU/allocation results are interpreter overhead,
not model speed. One 100-iteration run on the development machine emitted
2,643 bytes for the envelope versus 2,488 for the labelled read. This is one
fixture, not a general token saving or live latency claim. The entire read body
and its anchors are retained.

## Controlled task comparisons using existing settings

Do not silently retune defaults or issue billed calls. Obtain provider/cost
approval before live comparisons, especially Fast/Ultrafast tiers. Then:

1. Choose representative read-only investigation and implementation tasks with
   fixed input fixtures and explicit correctness checks. Use isolated workspace
   copies for implementation runs; preserve user changes. Keep provider/model,
   workflow, tool inventory, compaction and concurrency fixed unless they are
   the variable under test.
2. Record the effective scoped settings. Through the settings pane, vary only
   one of the following at a time; restore the previous workspace value after
   the comparison, and do not promote experimental settings globally:

   | Setting | Comparison |
   |---|---|
   | Model / reasoning effort (`codex_reasoning_effort`) | Supported `medium` vs current `high` |
   | Text verbosity (`text_verbosity`) | Supported `low` vs existing effective value |
   | Context / tool result max kb (`tool_result_max_kb`) | 32 or 64 vs current 512 KiB default |
   | Context / tool result max lines (`tool_result_max_lines`) | Hold fixed; current default 3,500 |
   | Limits / program parallel calls (`program_parallel_calls`) | Hold fixed; do not raise merely to fit a batch |
   | Model / speed (`service_tier`) | Only offered tiers, with explicit cost/eligibility acceptance |

3. Run several repetitions per variant, alternating order. Keep cold and warm
   cache scenarios separate. A fresh session/routing key does not prove the
   provider cache is cold; verify reported usage and label unknowns. For warm
   runs, preserve the session route and identical prefix, and record TTL gaps,
   mode changes, compactions and tool fingerprints. Do not mix root and child
   usage or add attempt usage to terminal totals again.
4. Record task duration, latest-attempt first output, attempt/provider/callback
   totals, completed-iteration prep/tools, reported prompt/completion/cache
   tokens, iterations, emitted bytes, truncations/spills, recovery reads and
   correctness. Capture attempt observations as they settle if a full
   per-request trace is needed; the UI intentionally retains only bounded
   aggregates and latest observations. Redact private tool content and never
   record credentials.
5. Compare p50/p95 end-to-end latency and total tokens alongside correctness and
   rereads. Lower effort/verbosity can reduce generated work without making
   decoding faster. Smaller output caps can lose if they cause repeated reads.
   Cost estimates may omit cache charges or speed-tier premiums.

Keep existing reasoning/output-cap defaults until the measured quality and
latency tradeoff supports a change. Do not retune compaction from Tiered's byte
threshold alone: the live path also applies a token-pressure gate.
