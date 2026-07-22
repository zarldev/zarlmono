---
title: zarlcode
description: Install and use the terminal coding agent built on zkit.
---

zarlcode is a local, inspectable terminal coding-agent workbench. It runs in the
workspace you launched it from, shows every model turn and tool call in a TUI,
and makes model/provider switching part of the workflow instead of a side quest.
Use it to plan read-only, build with visible file and shell tools, inspect diffs,
delegate focused sub-agents, run headless eval-like tasks, and resume local
sessions later.

![zarlcode in action](/zarlmono/zarlcode-hero2.gif)

## Install

```bash
# Homebrew installs the binary as zarlcode
brew install zarldev/tap/zarlcode
```

From a source checkout:

```bash
go tool task zarlcode
# or
go run ./zarlcode/cmd
```

> Note: `go install github.com/zarldev/zarlmono/zarlcode/cmd@latest` currently
> builds a binary named `cmd` because the CLI package directory is `cmd`. Use
> Homebrew, GitHub release archives, or `go tool task zarlcode` from a checkout
> until the public Go-install path is renamed or wrapped.

## First three minutes

### API-backed provider

```bash
zarlcode init
zarlcode keys set <provider> <key>   # anthropic, openai, gemini, deepseek, ...
zarlcode                            # run from the repository you want to work on
```

For OAuth-backed product surfaces, use the provider-specific flow instead:

```bash
zarlcode keys oauth claude-code
zarlcode keys oauth openai-codex
```

Run `zarlcode keys --help` for credential commands. Supported providers include
`anthropic`, `openai`, `deepseek`, `gemini`, `google-vertex`, `llamacpp`,
`ollama`, plus OAuth-backed `claude-code` and `openai-codex`.

### Local or OpenAI-compatible provider

zarlcode configures model endpoints but does not start model servers. Start
Ollama, llama.cpp, LM Studio, or another OpenAI-compatible server yourself, then
launch zarlcode and select or add the provider in **Settings → Providers**
(`Ctrl+S`) or the model picker (`Ctrl+E`).

### The loop to try first

1. Start in **Plan** mode and ask for a design before mutation.
2. Press `Shift+Tab` to switch to **Build** when you want edits or shell commands.
3. Watch tool calls, command output, changed files, and diffs in the timeline and working set.
4. Press `Ctrl+E` to switch provider/model without leaving the session.
5. Quit and resume later with `zarlcode -continue` from the same workspace.
6. For eval-like or scripted work, run the same task headlessly:

```bash
zarlcode --headless --prompt-file task.md
```

The workflow demo shows that full loop: Plan mode, model switching, Build, diff inspection, and a headless follow-up.

![zarlcode workflow demo: plan, switch model, build, inspect diff, and run headless](/zarlmono/zarlcode-workflow-demo.gif)

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

## How it works

zarlcode runs from the current workspace and keeps the run visible. The timeline
shows model output, tool calls, command results, diffs, plans, and sub-agent
summaries as they happen.

The core tools are the ones a coding agent needs every day: read files, make
anchored edits, search the tree, and run commands. Shell commands go through a
tracked process manager, so long-running commands can be inspected or stopped
instead of blocking the UI.

For larger tasks, zarlcode can split off focused sub-agents, compact older
history, and resume prior sessions from local SQLite state. Provider keys and
settings are stored locally under `~/.zarlcode`.

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
