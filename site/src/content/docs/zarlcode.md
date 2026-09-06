---
title: zarlcode
description: A local terminal coding agent with a TUI-first workflow for planning, building, inspecting, and resuming workspace changes.
---

zarlcode is a local, inspectable terminal coding-agent workbench. It runs in the
workspace you launched it from, keeps model turns and tool calls visible in a TUI, and
makes provider or model changes part of the normal workflow instead of a shell setup
task.

Use it to investigate in read-only Plan mode, build with visible file and shell tools,
inspect diffs, delegate focused sub-agents, and resume local sessions later.

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

## Start in the TUI

```bash
cd /path/to/your/workspace
zarlcode
```

On first run, press `Enter` to accept local defaults or `Ctrl+S` to configure a provider
from settings. Sessions open in Build mode; press `Shift+Tab` to investigate in read-only
Plan mode, then press it again when you want the agent to build. You do not need
`zarlcode init`, `keys`, or a headless flag to begin.

- [**First run and onboarding**](/zarlmono/zarlcode-onboarding/) — install, choose local
  defaults or configure a provider, and understand first-run vault setup.
- [**Your first workflow**](/zarlmono/zarlcode-workflow/) — Plan, Build, inspect diffs,
  and resume a local session.
- [**Interface guide**](/zarlmono/zarlcode-interface/) — timeline, cockpit, working set,
  file viewer, model picker, settings, and keys.

## What stays visible

The timeline shows model output, tool calls, command results, diffs, plans, and sub-agent
summaries as they happen. The coding toolset reads files, makes anchored edits, searches
the tree, and runs commands through a tracked process manager so long-running work can be
inspected or stopped.

For larger tasks, zarlcode can compact older model context, delegate focused sub-agents,
and resume prior sessions from local SQLite state. Its durable canonical transcript is
separate from compactable provider history, so browsing and Markdown export retain the
visible record after compaction.

- [**Sessions and transcripts**](/zarlmono/sessions-transcripts/) explains persistence,
  resume, and export.
- [**Providers and credentials**](/zarlmono/zarlcode-providers/) covers settings, models,
  OAuth, API keys, and the local credential vault.
- [**Safety and workspace access**](/zarlmono/zarlcode-safety/) explains Plan mode, Build
  mode, shell policy, sandboxing, and external tools.

## Build with confidence

zarlcode runs with your user's privileges. Its workspace tools and shell commands are
policy-guarded, and supported Linux systems can additionally sandbox shell execution, but
a coding agent can still mutate files and run processes. Review plans, tool calls, and
diffs when using powerful models or unfamiliar repositories.

For scripts, CI, credential subcommands, diagnostics, and one-shot headless work, use the
separate [automation and CLI reference](/zarlmono/zarlcode-automation/). Those advanced
surfaces remain supported without replacing the normal TUI journey.

## Built on zkit

zarlcode is built with the same Go packages available in [zkit](/zarlmono/getting-started/):

| Package | Role |
|---|---|
| `zkit/agent/runner` | The streaming agent loop. |
| `zkit/ai/tools/code` | Read, write, edit, shell, search, process, and plan tools. |
| `zkit/agent/guardrails` | Schema repair, shell policy, fan-out caps, and Go verifiers. |
| `zkit/agent/coderunner` | Standard coding toolset and guarded source assembly. |
| `zkit/prefs` and `zkit/vault` | Scoped settings and encrypted credential storage. |

The source lives at [`zarlcode/`](https://github.com/zarldev/zarlmono/tree/main/zarlcode).
