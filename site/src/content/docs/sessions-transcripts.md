---
title: Sessions and transcripts
description: How zarlcode keeps the visible timeline durable without coupling it to compactable model context.
---

zarlcode stores sessions locally in `~/.zarlcode/state.db`. A resumable session has two related but deliberately separate representations:

- the **canonical transcript** is the durable, renderer-independent record of what happened;
- the **model context** is the provider-facing message history used for the next completion.

The distinction matters because model context can be compacted, repaired, or reshaped to fit a provider. Those operations must not rewrite the session the user sees.

## What the canonical transcript records

The transcript is an ordered stream of typed entries. It preserves:

- user prompts and attachment metadata;
- streamed assistant and reasoning text, including interrupted turns;
- tool calls and terminal results;
- diffs and structured plan updates;
- loaded skills, notices, and compaction markers;
- sub-agent prompts, ownership, lifecycle state, and summaries.

The Bubble Tea timeline is a projection of this data rather than the persistence format. Resume rebuilds the timeline from the canonical entries, and Markdown export renders the same entries directly. UI layout details, ANSI styling, collapsed groups, and scroll position are not persisted as conversation truth.

## Durability during a live turn

Persistence follows the semantic importance of an event:

- submitted prompts, tool completion, plan/diff updates, sub-agent lifecycle changes, and completed turns are persisted immediately;
- streaming assistant and reasoning deltas are debounced to avoid a database write per token;
- save barriers flush queued transcript and session-state work before operations such as shutdown or deletion complete.

A sudden process or machine failure can still lose the newest unflushed streaming delta inside that short debounce window; semantic boundaries and orderly shutdown flush pending work.

Each transcript has a monotonically increasing revision and checksum. Entry updates and transcript metadata commit in one SQLite transaction. Reads also use one transaction so metadata and rows always come from the same snapshot.

Composer drafts are separate session state. Draft text is saved with its own debounce, restored on resume, and cleared after an accepted submission.

## Resume and interruption recovery

`zarlcode -continue` selects the latest session for the current workspace. The intro screen can resume any listed session.

On resume, zarlcode:

1. loads the session metadata and compactable model context;
2. verifies the canonical transcript revision and checksum;
3. validates entry ordering, parent relationships, and lifecycle states;
4. marks any interrupted assistant, reasoning, tool, or sub-agent work as interrupted;
5. rebuilds the visible timeline and restores plans, diffs, usage data, and any pending draft.

A session without a canonical transcript, or with a corrupt transcript, is rejected instead of being reconstructed from model context. There is intentionally no legacy fallback: model context is not an authoritative user-visible history.

## Compaction does not rewrite history

[Compaction](/zarlmono/compaction/) only changes the model context sent to later provider calls. It may summarize or trim older model messages while keeping tool-call linkage valid.

The canonical transcript remains unchanged, so browsing, resuming, and exporting a session do not lose earlier visible events when context pressure causes compaction.

## Export and deletion

`/export [path]` renders the canonical transcript as Markdown. With no path, exports go under `.zarlcode/exports/` in the workspace. Export never depends on the current TUI projection and never overwrites an existing file.

Deleting a session removes its transcript entries through SQLite foreign-key cascade along with the rest of that session's persisted state. The persistence queue uses a delete barrier so stale in-flight saves cannot recreate the deleted session.
