---
title: zarlcode interface
description: A screen-by-screen tour of the zarlcode TUI — the timeline, the cockpit, the working set, the file viewer, plan mode, and every key that opens them.
---

[zarlcode](/zarlmono/zarlcode/) is a full terminal application, not a
scrolling log. This page is the map: every screen it can show, what
each one is for, and the key that opens it. It's designed for a wide
terminal — at the home form factor (~220–280 columns) you get the
two-pane layout below; it degrades gracefully as the window narrows.

![zarlcode running a task](/zarlmono/zarlcode-hero2.gif)

## The layout

The default screen is four regions:

- **Timeline** (left / main) — the run transcript: your prompts, the
  assistant's streamed replies, tool calls, thinking, sub-agents.
- **Cockpit** (right sidebar, ~56 cols) — live context, cost, and tool
  telemetry for the session.
- **Composer** (bottom) — the input box, 3–8 lines, multi-line aware.
- **Status bar** (very bottom) — context-sensitive key hints on the
  left, transient toasts on the right.

The layout is responsive. At **≥160 columns** the cockpit sidebar
shows; below that it collapses and the timeline goes full-width, with
the run state folded into a compact text strip. Nothing disappears as
the window shrinks — it reflows.

When you launch `zarlcode` with no flags you land on the **intro
screen**: a prompt box and a picker of saved sessions. Type a task and
press `Enter` to start fresh, or pick a prior session to resume. (The
overlay shortcuts below only activate once you're past the intro, in
the main UI.)

## The timeline

The timeline is a vertical list of typed items, rendered tail-first so
the newest is at the bottom and the view follows the stream live. Item
kinds include:

- **prompts** and **assistant turns** (streamed markdown),
- **tool calls** and their results, formatted per tool (a `bash`
  command, a file path, a diff),
- **thinking** blocks — collapsible reasoning,
- **groups** — per-iteration bundles of tools/edits, collapsed behind a
  `[+]` toggle,
- **sub-agents** — a spawned run nested inline,
- **diffs**, **plans**, and **notices** (e.g. a compaction marker).

Press **`Tab`** to enter **browse mode**: the view freezes, arrow keys
move the selection item-by-item, and `Space`/`Enter` collapse or expand
the selected item. `Esc` (or `i`) returns to the live tail. This is how
you scroll back through a long run, fold away noisy tool output, or
open a sub-agent to see what it did.

## The cockpit and dashboard

The sidebar **cockpit** is a live readout of the run: provider and
model, the context window and compaction threshold, a role-partitioned
**context gauge** (system / user / assistant / tool / free), the last
turn's flow (tokens in/out, iterations, duration, tok/s), session cost
with cache savings, and a histogram of the tools used so far.

Press **`Ctrl+L`** to expand it into the full-screen **dashboard** —
the same metrics at scale across one to three responsive columns:
context composition, sparklines for throughput / cache-hit / cost, and
a per-turn **history table** (with a `↯` marker on turns where
compaction fired). Arrow keys and `PageUp`/`PageDown` scroll it; `Esc`
or `Ctrl+L` closes it.

![The expanded dashboard](/zarlmono/zarlcode-cockpit.gif)

## The working set

Press **`Ctrl+W`** for the **working set** — every file the agent has
mutated this session, plus the turns that changed them. The left column
toggles between a **Files** view and a **Turns** view (`Tab`); the right
shows the corresponding diffs.

From here, `Enter` opens the full **diff browser** for the selected
file or turn, `o` opens the file in your editor, and **`r`** rolls the
file or turn back to its checkpoint — zarlcode snapshots file state per
turn, so a bad edit is undoable without `git`.

![The working set and diff browser](/zarlmono/zarlcode-workingset.gif)

## The file viewer

Press **`Ctrl+F`** for a full-screen, read-only **file viewer** with
four tabs (`Tab` to cycle):

- **Files** — a directory tree on the left, file preview on the right.
- **Skills** — the workspace's discovered [skills](/zarlmono/foundation/#shared-infrastructure).
- **Agents** — the named sub-agent profiles available to `spawn_agent`.
- **Hooks** — configured command hooks.

Arrow keys move, `Enter` descends into a directory (or jumps to a
definition's source), `o` opens the selected file in your editor, and
`Esc` closes. It's the fastest way to see what the agent can see.

![The file viewer](/zarlmono/zarlcode-fileviewer.gif)

## Plan and build mode

Press **`Shift+Tab`** to toggle between **build** mode (the default —
full tool surface) and **plan** mode. In plan mode the UI tint shifts,
the agent's tool surface goes read-only, and it produces a structured,
reviewable plan instead of editing. Toggle back to build when the plan
looks right. `Ctrl+P` opens the plan pane to review live and saved
plans (the latter persist under `.zarlcode/plans/`).

![Plan mode](/zarlmono/zarlcode-planmode.gif)

## Models, providers, and themes

- **`Ctrl+E`** — the **model picker**: provider tabs across the top
  (`Tab`/`←`/`→`), a scrollable model list, and a free-text fallback for
  a model name the list doesn't have. Selecting re-points the live
  runner mid-session.
- **`Ctrl+S`** — **settings**: a master-detail pane for providers (keys
  in the vault, OAuth sign-in, custom OpenAI-compatible endpoints),
  appearance, agents, skills, hooks, and MCP servers.
- **`Ctrl+T`** — the **theme picker**, with live preview as you move
  through the list.

![The model picker](/zarlmono/zarlcode-modelpicker.gif)

## Sub-agents

When a turn delegates with [`spawn_agent`](/zarlmono/spawn/), each child
run appears as a **collapsible sub-agent item** in the timeline, nested
under the turn that spawned it — its own prompt, tool calls, and final
summary, foldable in browse mode. A coordinator fanning out to
`explore` workers reads as a tidy tree rather than a wall of
interleaved output.

![Sub-agents in the timeline](/zarlmono/zarlcode-subagents.gif)

## Keybinding reference

| Key | Action |
|---|---|
| `Enter` | submit the prompt |
| `Shift+Enter` / `Ctrl+J` | newline in the composer |
| `↑` / `↓` | browse input history (in the composer) |
| `Tab` | enter timeline browse mode |
| `Esc` | stop the running turn / leave browse / close an overlay |
| `Shift+Tab` | toggle plan ⇄ build mode |
| `Ctrl+L` | expand / collapse the context dashboard |
| `Ctrl+W` | working set (changed files & turns) |
| `Ctrl+F` | file viewer (Files / Skills / Agents / Hooks) |
| `Ctrl+E` | model & provider picker |
| `Ctrl+S` | settings |
| `Ctrl+T` | theme picker |
| `Ctrl+P` | plan pane |
| `Ctrl+K` | agents & skills catalog |
| `Ctrl+G` | key help |
| `Ctrl+Q` | clear context (with confirm) |
| `Ctrl+I` | inspector |
| `Ctrl+C` | quit |

Slash commands work in the composer too: `/clear` resets the
conversation, `/help` opens the key help. Press `Ctrl+G` any time for
the full, context-aware list.

## Launch flags

| Flag | Effect |
|---|---|
| *(none)* | interactive TUI starting on the intro screen |
| `--continue` | resume the previous session in this workspace |
| `--agent <name>` | start with a named agent profile (`agents/<name>.md`) |
| `--env <path>` | load a `.env` file before reading config |
| `--headless` | run one task to completion with no TUI, recording the result to `state.db` |
| `--prompt-text <s>` / `--prompt-file <p>` | the task prompt for `--headless` |
| `--max-iter <n>` | override the iteration cap for `--headless` (0 = config default) |
