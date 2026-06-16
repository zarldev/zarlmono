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

## Where to find it

The source lives at [`swebench-eval/`](https://github.com/zarldev/zarlmono/tree/main/swebench-eval).
