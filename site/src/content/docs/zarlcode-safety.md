---
title: Safety and workspace access
description: Understand zarlcode's user-privilege boundary, Plan mode, workspace tools, shell policy, and external integrations.
---

zarlcode runs with **your user privileges**. It is a local coding agent, not an isolated
hosted service: Build mode can edit workspace files and run processes. Treat a model's
requests as suggestions to review, particularly in an unfamiliar repository.

## Start with Plan mode

Plan mode is the safe default for investigation. The agent can inspect the workspace and
produce a structured proposal, but it cannot edit files or run shell commands. Review the
plan in the timeline or with `Ctrl+P`, then use `Shift+Tab` to enter Build mode only when
you want execution.

This is a workflow guardrail, not a substitute for reviewing a change. Use the visible
timeline and working set to understand what Build mode did.

## Workspace tools and shell commands

The coding tools operate relative to the workspace you launched from. File reads, edits,
searches, and directory listing are routed through the agent tool system; each shell
command is tracked so it can be inspected or stopped instead of disappearing into terminal
scrollback.

Open **`Ctrl+W`** to inspect changed files, diffs, and tracked processes. The working set
can roll a file or turn back to its recorded checkpoint. Use `Ctrl+F` to see the files,
skills, agents, and hooks that are visible to the session.

Shell policy and process guardrails apply to Build-mode commands. On supported Linux
systems, sandboxing can additionally confine shell execution with Landlock. It is a
useful layer, not a guarantee that removes the need for a suitably scoped user account or
review.

## Network, MCP, and sub-agents

Web tools, MCP servers, and other external integrations can access systems beyond the
workspace according to their own configuration and credentials. Confirm which MCP servers
and tools are enabled before giving a task authority over sensitive systems.

Sub-agents are separate runs owned by the parent task. They make focused research or
verification easier to inspect, but they share the same trust boundary; conflicting
workspace writes are refused rather than raced.

For the implementation detail behind these controls, see [The tool system](/zarlmono/tools/),
[Guardrails](/zarlmono/guardrails/), and [Sandboxing](/zarlmono/sandboxing/).
