---
title: zarlcode
description: A terminal coding agent built on zkit — runner, guardrails, sandboxed shell, sub-agents, and SQLite-backed sessions.
---

zarlcode is a terminal UI coding agent. It is one of the real products
shipped in this repo, built almost entirely from zkit packages.

![zarlcode in action](/zarlmono/zarlcode-hero2.gif)

## What it does

- Runs an interactive agent loop in a TUI
- Reads, writes, edits, and patches files in a workspace-rooted sandbox
- Executes shell commands through a sandboxed process manager
- Selects and calls tools with guardrails and fan-out budgets
- Persists sessions, settings, and API keys in SQLite
- Spawns read-only or verify-only sub-agents for exploration and testing

For a screen-by-screen tour of the TUI — the timeline, the cockpit, the
working set, the file viewer, plan mode, and every key that opens them —
see [the interface reference](/zarlmono/zarlcode-interface/).

## Key zkit packages it uses

| Package | Role |
|---|---|
| `zkit/agent/runner` | The streaming agent loop |
| `zkit/ai/tools/code` | `read`, `write`, `edit`, `bash`, `grep`, `ls`, and plan tools |
| `zkit/agent/guardrails` | Schema repair, shell policy, fan-out caps, Go verifiers |
| `zkit/agent/coderunner` | Standard coding toolset + guarded source assembly |
| `zkit/agent/spawn` | Sub-agent delegation |
| `zkit/prefs` | Encrypted API keys and workspace-scoped settings |

## Where to find it

The source lives at [`zarlcode/`](https://github.com/zarldev/zarlmono/tree/main/zarlcode).

```bash
cd zarlcode
go run ./cmd
```
