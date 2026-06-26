---
title: zarlcode
description: Install and use the terminal coding agent built on zkit.
---

zarlcode is a terminal coding agent. It runs in the workspace you launched it
from, shows every model turn and tool call in a TUI, and lets you switch
between read-only planning and file-changing build work. It can read and edit
files, run commands, search the web, connect MCP tools, and delegate focused
work to sub-agents.

![zarlcode in action](/zarlmono/zarlcode-hero2.gif)

## Install

```bash
# latest tagged release
go install github.com/zarldev/zarlmono/zarlcode/cmd@v0.1.4

# or Homebrew
brew install zarldev/tap/zarlcode
```

From a source checkout:

```bash
go tool task zarlcode
# or
go run ./zarlcode/cmd
```

## First run

```bash
zarlcode init
zarlcode keys set <provider>   # anthropic, openai, gemini, deepseek, ...
zarlcode
```

Supported providers include `anthropic`, `openai`, `deepseek`, `gemini`,
`google-vertex`, `llamacpp`, `ollama`, plus OAuth-backed `claude-code` and
`openai-codex`.

Common commands:

```bash
zarlcode                               # interactive TUI
zarlcode -continue                     # resume the last session in this workspace
zarlcode --headless --prompt-file t.md # run one task without the TUI
zarlcode keys list                     # view configured provider keys, masked
zarlcode upgrade                       # self-update from GitHub Releases
```

For a screen-by-screen tour of the TUI — timeline, cockpit, working set, file
viewer, plan mode, model picker, settings, and every key that opens them — see
[the interface reference](/zarlmono/zarlcode-interface/).

## What it does

- Runs an interactive agent loop in a full terminal UI.
- Reads, writes, edits, and patches files through workspace-bounded tools.
- Executes foreground and background shell commands through a tracked process manager.
- Uses guardrails for schema repair, shell policy, fan-out caps, and verifier feedback.
- Persists sessions, settings, plans, and encrypted API keys locally.
- Spawns read-only, verify-only, or implementation sub-agents for focused work.
- Runs headless for scripts, CI jobs, and eval harnesses.

## Plan mode and build mode

zarlcode has two work modes:

| Mode | What happens |
|---|---|
| **Plan** | Read-only investigation. The agent can inspect the workspace and produce a structured plan, but cannot edit files or run shell commands. |
| **Build** | Full tool surface: file edits, shell, web, MCP, plans, and sub-agents, still routed through guardrails. |

Toggle with `Shift+Tab`. Use Plan when you want a design before mutation; switch
to Build when you want the agent to execute.

## Key zkit packages it uses

| Package | Role |
|---|---|
| `zkit/agent/runner` | The streaming agent loop. |
| `zkit/ai/tools/code` | `read`, `write`, `edit`, `bash`, `grep`, `ls`, process, and plan tools. |
| `zkit/agent/guardrails` | Schema repair, shell policy, fan-out caps, Go verifiers. |
| `zkit/agent/coderunner` | Standard coding toolset + guarded source assembly. |
| `zkit/agent/tools/spawn` | Sub-agent delegation. |
| `zkit/prefs` | Encrypted API keys and scoped settings. |

## Trust boundary

zarlcode runs with your user's privileges. Its tools are workspace-scoped and
policy-guarded, and sandboxing can confine shell commands on supported Linux
systems, but it is still a tool that can mutate files and execute processes.
Review tool calls when using powerful models or unfamiliar repositories.

## Source

The source lives at [`zarlcode/`](https://github.com/zarldev/zarlmono/tree/main/zarlcode).
