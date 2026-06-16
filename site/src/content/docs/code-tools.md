---
title: Code tools
description: The workspace-scoped toolset a coding agent actually needs, including a process manager for long-running commands.
---

`zkit/ai/tools/code` ships the standard coding toolset. Every tool
is workspace-rooted — paths resolve inside the workspace, not the
host filesystem — and each one is shaped by watching agents work,
not by API symmetry. Two more tools live in sibling packages:
[`web_fetch`](#web-fetch-and-web-search) (`zkit/ai/tools/fetch`) and
[`web_search`](#web-fetch-and-web-search) (`zkit/ai/tools/search`).
| Tool | Notes |
|---|---|
| `read` | File reads with offset/limit + stable line hashes for anchored edits. Pure → cached by MemoSource. |
| `write` | **Creates only — refuses to overwrite.** Existing files are rejected with a validation error naming `edit` as the recovery path. |
| `write_append` | Append-only writes, so intent is explicit. Also the structural fix for large files: scaffold with `write(path, "")`, then chunk via repeated appends. |
| `edit` | String-replace edits with line/hash anchors from `read` output. Whitespace-sensitive failure messages help the model fix its own near-miss matches. |
| `apply_patch` | Unified-diff application. |
| `grep` | Workspace-rooted search with glob filtering. Pure → cached. |
| `glob` | Workspace path enumeration by glob pattern. Returns labelled or JSON output. |
| `ls` | Structured single-level directory listing. Pure → cached. |
| `bash` | Shell execution, backed by the process manager (below). Foreground commands return inline; long-running commands return a process ID. |
| `bash_output` | Read accumulated stdout/stderr from a background process, with cursor-based polling. |
| `stop_process` | Terminate a background process (SIGTERM with grace, then SIGKILL). |
| `list_processes` | Inventory of tracked processes — running and recently exited. |
| `save_plan` | Persist a plan-mode artifact to `.zarlcode/plans/<name>.md`. Scaffolds the plan file. |
| `save_plan_append` | Append chunks to a plan file — the structural partner to `write_append` for plan documents. |
| `update_plan` | Update the live structured plan rendered in the TUI plan pane. Drives step tracking (pending → in_progress → completed). |
## Why write refuses to overwrite

This is a runtime invariant, not prompt guidance: the tool
*physically cannot* clobber an existing file, so a model whose
instinct is "rewrite the whole file" is forced through the
read → edit loop instead. Small-model benchmark runs show the
refusal fires on a substantial fraction of exercises and
consistently improves correctness — wholesale rewrites of existing
files are rarely the right move and frequently destroy code the
model never read.

## bash and the process manager

`bash` doesn't just exec and block — that would wedge the loop the
first time the model starts a dev server.

1. Short-lived commands run synchronously and return output inline.
2. Commands still running at the timeout get a process ID; output is
   teed to a ring buffer and the tool returns "process started, use
   `bash_output` to read".
3. `bash_output(id)` reads the buffer; `stop_process(id)` terminates
   (SIGTERM, then SIGKILL after grace); `list_processes` is the
   inventory.

The agent can run `npm run dev` or a long test loop without losing
the conversation. `ProcessManager` owns every goroutine and
`KillAll()` on shutdown means no orphan children outlive the host.

## Sandboxing

Shell execution composes with `zkit/agent/sandbox`: kernel-enforced
confinement via a Landlock filesystem allow-list plus an optional
empty network namespace. The sandbox is enforced by the kernel, not
by string-matching the command — `bash` runs whatever it runs, and
the kernel says no to anything outside the allow-list. (Linux only;
on other platforms the policy guardrail below is the only line of
defence.)

The [shell-policy guardrail](/zarlmono/guardrails/) is the advisory
layer in front of this: it rejects the obviously destructive
patterns before they execute, sandbox or not.

## The plan system

Three tools form a plan persistence and display pipeline:

- **`save_plan`** creates `.zarlcode/plans/<name>.md` — a markdown
  artifact the user can revisit, edit, or share. Writes are locked to
  the plans directory.
- **`save_plan_append`** appends chunks — the structural fix for
  plans that exceed the single-call content cap, analogous to
  `write_append` for code files.
- **`update_plan`** feeds the TUI's structured plan pane: an ordered
  list of steps each in `pending`, `in_progress`, or `completed`
  state. The plan pane renders live as the agent works.

Together they let the agent produce a plan in PLAN mode, then track
execution step-by-step in BUILD mode — all visible to the user through
the TUI without reading raw tool output.

## web_fetch and web_search

These live in sibling packages under `zkit/ai/tools/` rather than
`code/` because they aren't workspace-scoped, but they register into
the same registry:

- **`web_fetch`** (`zkit/ai/tools/fetch`) — HTTP GET with automatic
  chromedp fallback for JavaScript-heavy pages. SSRF protection via
  transport-level IP dial guard. Capped output (200k chars).
- **`web_search`** (`zkit/ai/tools/search`) — queries a local
  SearXNG instance. Returns labelled or JSON output. Default 10
  results, hard cap 25.