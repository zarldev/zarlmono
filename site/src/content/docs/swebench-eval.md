---
title: swebench-eval
description: A SWE-bench evaluation driver built on zkit — the same agent assembly used by the TUI, measured against real issues.
---

swebench-eval runs the zkit-based agent against SWE-bench tasks. It exists
so the framework is tested the same way it is shipped: by solving real
GitHub issues end-to-end.

## What it does

- Loads a SWE-bench instance
- Builds the agent from the same packages zarlcode uses
- Applies the agent's edits to the repository
- Runs the instance's test command
- Records whether the patch passes

## Key zkit packages it uses

| Package | Role |
|---|---|
| `zkit/agent/coderunner` | Standard coding toolset + guardrails |
| `zkit/agent/runner` | The streaming loop |
| `zkit/agent/guardrails` | Schema repair, shell policy, verifiers |
| `zkit/agent/pursue` | Verified completion against test results |
| `zkit/ai/tools/code` | Workspace-scoped file and shell tools |

## Why it matters

Because swebench-eval and zarlcode share `coderunner.GuardedSource`, a
change to guardrails or tool dispatch is exercised in both an interactive
TUI and a headless eval harness. The interfaces stay honest because they
have more than one consumer.

## Build and version

```bash
go tool task sweeval
nix/bin/sweeval -version
```

Release builds report the module tag. Source installs made with `go install ...@version`
use Go module build metadata; local builds fall back to the VCS revision.

## Verified re-drive telemetry

`--zarlcode-verified-attempts N` with `N > 1` verifies each candidate patch and
re-drives rejected attempts. The eval database records whether the in-run goal accepted
an attempt, how many attempts were consumed, and the ordered JSON verifier verdicts.

Rows created before migration `00004` have zero/empty defaults. Those values mean
verification telemetry was not recorded for the historical row; they must not be
reported as a newly verified failure. The detailed commands and migration notes live in
[`swebench-eval/README.md`](https://github.com/zarldev/zarlmono/tree/main/swebench-eval#readme).

## Where to find it

The source lives at [`swebench-eval/`](https://github.com/zarldev/zarlmono/tree/main/swebench-eval).
