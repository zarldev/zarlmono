---
title: Documentation
description: Guides and reference for zarlcode, the zkit Go toolkit, and the applications built with it.
---

Use **zarlcode** to work on a codebase from your terminal, or use **zkit** to build an agent into your own Go application. These docs cover setup, daily workflows, customization, runtime behavior, and the boundaries of agent execution.

:::note[V2 preview]
This is the documentation entry point for the isolated V2 preview, not a live-site switchover. Existing guides remain available at their original URLs.
:::

## Choose a starting point

- **I want to use the coding agent.** [Install and configure zarlcode](/zarlmono/zarlcode-onboarding/), then [plan, build, and review a change](/zarlmono/zarlcode-workflow/).
- **I want to build an agent in Go.** Follow the [zkit quickstart](/zarlmono/getting-started/), then use the [architecture map](/zarlmono/architecture/) to choose the packages you need.
- **I want to understand what runs on my machine.** Read [Safety and workspace access](/zarlmono/zarlcode-safety/) before enabling tools or handing off a task.
- **Something is not working.** Start with [Troubleshooting](/zarlmono/troubleshooting/) for provider, credential, session, and automation checks.

## Use zarlcode

### Setup and configuration

- [Product overview](/zarlmono/zarlcode/) — installation options and the main capabilities of the terminal application.
- [Install and first run](/zarlmono/zarlcode-onboarding/) — launch in a workspace, select a local or hosted model, and complete provider setup.
- [Providers and credentials](/zarlmono/zarlcode-providers/) — model selection, the global credential vault, and global versus workspace settings.

### Work on a codebase

- [Your first workflow](/zarlmono/zarlcode-workflow/) — investigate in Plan mode, switch to Build, run checks, inspect the diff, and resume later.
- [Interface guide](/zarlmono/zarlcode-interface/) — timeline, cockpit, working set, file viewer, model picker, sub-agents, and keyboard shortcuts.
- [Sessions and transcripts](/zarlmono/sessions-transcripts/) — local persistence, interruption recovery, compaction, export, and deletion.
- [Automation and CLI](/zarlmono/zarlcode-automation/) — headless tasks, named-agent launch options, initialization, diagnostics, and updates.

### Customize and control execution

- [Files and workspace resources](/zarlmono/zarlcode-interface/#the-file-viewer) — inspect the files, skills, agents, and hooks visible to the session.
- [Sub-agent tasks](/zarlmono/spawn/) — named profiles, work modes, task lifecycle, and workspace coordination. This is the shared runtime reference; the [TUI guide](/zarlmono/zarlcode-interface/#sub-agents) covers the task panel.
- [MCP connections](/zarlmono/tool-ecosystem/#mcp-connections) — external tool discovery, connection policy, and name-collision boundaries.
- [Web search services](/zarlmono/tool-ecosystem/#search--web_search) — SearXNG and Brave backends, including where to configure them in zarlcode.
- [Safety and workspace access](/zarlmono/zarlcode-safety/) — user privileges, Plan mode, shell execution, network integrations, and rollback.

## Build with zkit

### Assemble an agent

- [Toolkit overview](/zarlmono/toolkit/) — the Go packages behind zarlcode and where they fit in an application.
- [Quickstart](/zarlmono/getting-started/) — a complete minimal agent with a provider, typed tool, and runner.
- [Architecture](/zarlmono/architecture/) — package boundaries, composition, and extension points.
- [Runnable examples](/zarlmono/examples/) — focused harnesses, including deterministic modes that need no live model.

### Models and tools

- [LLM providers](/zarlmono/providers/) — adapters, backend registration, chat templates, recovery, and conformance tests.
- [Tool system](/zarlmono/tools/) — interfaces, typed arguments, schemas, registry behavior, tool effects, and output formats.
- [Code tools](/zarlmono/code-tools/) — workspace file operations, managed shell processes, planning, and web tools.
- [Tool ecosystem](/zarlmono/tool-ecosystem/) — typed tool construction, runtime tools, MCP, fetching, and search.

### Runtime, state, and verification

- [Runner](/zarlmono/runner/) — construction, results, streaming events, prompt sources, and terminal conditions.
- [Sub-agent tasks](/zarlmono/spawn/) — delegation, cancellation, iteration caps, and task ownership.
- [Compaction](/zarlmono/compaction/) — structural, tiered, and model-assisted approaches to fitting history into a context budget.
- [Shared infrastructure](/zarlmono/shared-infra/) — retrieval, indexing, workflows, checkpoints, human review, and local session storage.
- [Verified completion](/zarlmono/pursue/) — re-drive attempts against explicit goals and verifier feedback.

### Safety and package reference

- [Guardrails](/zarlmono/guardrails/) — tool-dispatch middleware, schema repair, shell policy, and post-edit verification.
- [Sandboxing](/zarlmono/sandboxing/) — the distinction between static shell checks and kernel-enforced confinement, including platform limitations.
- [Foundation packages](/zarlmono/foundation/) — supporting packages for configuration, credentials, infrastructure, and applications.

A library capability is not automatically enabled in every application. Consult the application guide for its configured behavior; consult the zkit reference when assembling your own runtime.

## Examples and evaluation

- [Feature coverage](/zarlmono/feature-coverage/) — find an example that exercises a particular capability.
- [SWE-bench evaluation](/zarlmono/swebench-eval/) — the evaluation driver, its zkit dependencies, build commands, and re-drive telemetry.

Use the documentation search for a command, package, or feature name. For CLI flags, `zarlcode --help` and the relevant subcommand's `--help` describe the version installed on your machine.
