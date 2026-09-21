---
title: Turn lifecycle and settlement
description: Follow a zarlcode turn through execution, event application, durable saving, recovery, rewind, and shutdown.
---

This guide connects the interactive turn's runtime, event, and persistence
boundaries. Use it when changing submission, cancellation, queued input, session
saves, or rewind. [Architecture](https://github.com/zarldev/zarlmono/blob/main/zarlcode/docs/architecture.md) covers extension seams;
the [runner identity and history contract](https://github.com/zarldev/zarlmono/blob/main/zkit/agent/runner/HIERARCHY.md)
defines replay, execution identity, and usage accounting.

## Owners and authorities

| Owner | Responsibility | Completion proves |
| --- | --- | --- |
| `LiveRunner` admission and `RuntimeReservation` | Coordinate turns, mutable operations, exclusive restore, and shutdown. | Conflicting runtime work is excluded or has drained. |
| `ContextCache` | Serialize context transitions and retain the next turn's model messages. | The engine has published its resulting context, including useful partial results. |
| Turn-owned spawn group | Own and join child tasks. | Children no longer use the turn's dependencies. |
| `teasink.Sink` | Deliver events and ordered application markers. | Handling a marker in `UI.Update` proves preceding events were applied. |
| `UI.Update` and its persistence FIFO | Own interactive operation state, snapshots, save ordering, and acknowledgments. | A successful full-save acknowledgment binds the applied transcript to the completed context. |
| `zkit/db` transactions | Commit session state and enforce expected content versions. | The transaction's returned receipt identifies the committed version. |

Runtime quiescence, applied events, and durable state are separate observations.
`ConversationEnded` bookends one `Runner.Run`; it does not establish all three.
Likewise, a sink `Drain` only acknowledges delivery to `Program.Send`, not
application by the UI. See [runtime admission](https://github.com/zarldev/zarlmono/blob/main/zarlcode/engine/runtime_reservation.go),
[context transitions](https://github.com/zarldev/zarlmono/blob/main/zarlcode/engine/context_cache.go), and
[event barriers](https://github.com/zarldev/zarlmono/blob/main/zarlcode/tui/teasink/applied_barrier.go).

## Normal interactive path

The following steps describe a storage-backed turn with exact checkpoint
protection. Save failures and legacy sessions have the distinct paths described
below.

1. **Reserve and protect the input.** `runLiveTurnInput` creates an operation
   identified by session and generation. `enqueueBeforeTurn` reserves the
   runtime, captures the transcript boundary and observed source version, and
   transfers reservation ownership to the persistence FIFO. The BEFORE
   transaction saves the checkpoint and submitted prompt, including attachments,
   before dispatch. A queued prompt remains queued until turn setup succeeds.
   See [submission](https://github.com/zarldev/zarlmono/blob/main/zarlcode/tui/live_settlement.go) and
   [BEFORE dispatch](https://github.com/zarldev/zarlmono/blob/main/zarlcode/tui/rewind_dispatch.go).

2. **Prepare and run.** The engine builds the target and dependencies before
   converting the exclusive reservation into turn admission. The context gate
   serializes the transition and permits cancellation while waiting. Each turn
   owns its spawn group. See [turn execution](https://github.com/zarldev/zarlmono/blob/main/zarlcode/engine/live_turn.go),
   [reservation conversion](https://github.com/zarldev/zarlmono/blob/main/zarlcode/engine/runtime_reservation.go), and
   [turn assembly and cleanup](https://github.com/zarldev/zarlmono/blob/main/zarlcode/engine/live_tools.go).

3. **Capture history during execution.** `sessionHistorySink.Append` serializes
   observations into owned batches. `Request` commits pending batches and the
   prepared model request before invoking the provider. Prepared requests are
   runner-level inputs, not provider wire captures. Batch IDs and bytes remain
   available through final save and retry; acknowledgment releases only committed
   batches. See [history capture](https://github.com/zarldev/zarlmono/blob/main/zarlcode/engine/session_history.go).

4. **Settle the engine and events.** Runner termination is followed by child
   drain and context publication before the engine call returns. The command then
   enqueues its completion marker through `AfterEvents`. The UI handles preceding
   events before that marker, checks operation identity, and captures the full
   session snapshot. Delayed results from another session or generation cannot
   settle the current operation. See [settlement](https://github.com/zarldev/zarlmono/blob/main/zarlcode/tui/live_settlement.go) and
   [BEFORE command completion](https://github.com/zarldev/zarlmono/blob/main/zarlcode/tui/rewind_dispatch.go).

5. **Commit and acknowledge.** The FIFO preserves the full-save boundary even
   though adjacent transcript-only snapshots can be coalesced. The database
   transaction commits session context, transcript, and captured history. Its
   returned content version becomes the local write receipt; rereading the row
   afterward could accidentally adopt another writer's changes. The UI releases
   operation ownership when it applies the successful acknowledgment. Automatic
   queue promotion occurs only on the eligible success path, not after a failed
   turn or a save recovery. See [save ordering](https://github.com/zarldev/zarlmono/blob/main/zarlcode/tui/draft_persist.go),
   [snapshot construction](https://github.com/zarldev/zarlmono/blob/main/zarlcode/tui/session_persist.go), and
   [transaction receipts](https://github.com/zarldev/zarlmono/blob/main/zarlcode/tui/local_write_receipt.go).

## Failure and recovery paths

| Boundary | Result |
| --- | --- |
| Reservation or BEFORE save rejected | No provider dispatch. Release the reservation and retain queued input or restore submitted input without overwriting newer composer edits. |
| Terminal runner error or cancellation | The engine retains returned context and drains children. Individual tool failures can be returned to the model without terminating the loop. |
| History capture fails | Prevent further provider requests. Already performed external tool effects cannot be rolled back by a save. |
| Full settlement save fails | Preserve the last durable exact head and report unsaved state. Release UI operation ownership so explicit input can continue; queued input is not automatically dispatched. |
| Competing source write | Reject the stale expected content version and surface a conflict. A newer database row is not permission to overwrite it. |
| Recovery save succeeds | Acknowledge the protected completed boundary and clear unsaved state. Previously queued input still requires explicit submission. |

Continuing after a save failure does not promise resumability of the unsaved
suffix. Recorded turns can still attempt history writes, but the session is not
fully settled until its coordinated full save succeeds. Recovery retains the
trusted source observation and exact captured bytes. A crash can lose pending
data. See [save recovery](https://github.com/zarldev/zarlmono/blob/main/zarlcode/tui/session_recovery.go) and
[settlement failure handling](https://github.com/zarldev/zarlmono/blob/main/zarlcode/tui/live_settlement.go).

The normal diagram is also not an automatic upgrade path for nonempty legacy
sessions: `enqueueBeforeTurn` rejects a session without exact checkpoint
protection and directs the user to start a new conversation.

## Rewind and shutdown

Rewind requires the current turn, applied transcript, and queued writes to be
settled. Preview captures a selection and source version; activation revalidates
them, reserves the runtime, checks its fingerprint, and saves the source before
creating a durable child branch. It then publishes the restored context, target,
plan, and UI state. If the child commits but runtime restoration fails, the UI
requires restart to recover the durable active child. Rewind preserves current
security policy and does not undo files, processes, network calls, or other
external effects. See [branch activation](https://github.com/zarldev/zarlmono/blob/main/zarlcode/tui/rewind_activate.go) and
[checkpoint semantics](https://github.com/zarldev/zarlmono/blob/main/zarlcode/rewind/checkpoint.go).

Shutdown must also account for commands that Bubble Tea returned but never
started. `FlushSessionPersistence` claims such commands or cancels and joins
started ones, preserving recovery input without dispatching it. A timed-out
flush retains ownership for a later join. The composition root registers
persistence cleanup ahead of closing the live runner, sink, process manager, and
settings in shutdown order. An expired cleanup budget must not close dependencies
still in use. See [persistence drain](https://github.com/zarldev/zarlmono/blob/main/zarlcode/tui/session_persist.go) and
[launch cleanup](https://github.com/zarldev/zarlmono/blob/main/zarlcode/tui/launch.go).

`LiveRunner.Close` begins one owned shutdown operation, rejects new work once
closing starts, cancels the active turn, waits for admission to drain, and closes
owned resources. An outstanding exclusive reservation can reject closing with
`ErrRuntimeBusy`; its owner must release it. A caller deadline bounds the wait,
not the underlying shutdown. The composition root owns the borrowed process
manager. See [runner shutdown](https://github.com/zarldev/zarlmono/blob/main/zarlcode/engine/live_lifecycle.go).

Headless execution shares runtime admission, context transitions, and child
drain, but uses its own recorder and optional verified re-drive. It does not
pass through the interactive UI marker and save-acknowledgment sequence.
See [headless execution](https://github.com/zarldev/zarlmono/blob/main/zarlcode/engine/headless.go).

## Tests to read when changing a boundary

| Contract | Existing coverage |
| --- | --- |
| Cancellation while waiting for context | [context_wait_test.go](https://github.com/zarldev/zarlmono/blob/main/zarlcode/engine/context_wait_test.go) |
| Child drain before closing dependencies | [turn_drain_test.go](https://github.com/zarldev/zarlmono/blob/main/zarlcode/engine/turn_drain_test.go) |
| History snapshots, retries, and acknowledgment | [session_history_test.go](https://github.com/zarldev/zarlmono/blob/main/zarlcode/engine/session_history_test.go) |
| Delivery versus application | [applied_barrier_test.go](https://github.com/zarldev/zarlmono/blob/main/zarlcode/tui/teasink/applied_barrier_test.go) |
| Full save before queue promotion | [live_settlement_test.go](https://github.com/zarldev/zarlmono/blob/main/zarlcode/tui/live_settlement_test.go) |
| Explicit continuation after save failure | [live_settlement_recovery_test.go](https://github.com/zarldev/zarlmono/blob/main/zarlcode/tui/live_settlement_recovery_test.go) |
| Competing writes and local receipts | [local_write_receipt_test.go](https://github.com/zarldev/zarlmono/blob/main/zarlcode/tui/local_write_receipt_test.go) |
| Stale preview and source rejection | [rewind_source_version_test.go](https://github.com/zarldev/zarlmono/blob/main/zarlcode/tui/rewind_source_version_test.go) |
| Rejected activation preserves runtime | [rewind_activation_safety_test.go](https://github.com/zarldev/zarlmono/blob/main/zarlcode/tui/rewind_activation_safety_test.go) |
| Never-started commands, backpressure, and timed-out shutdown | [rewind_dispatch_shutdown_test.go](https://github.com/zarldev/zarlmono/blob/main/zarlcode/tui/rewind_dispatch_shutdown_test.go) |

From the repository root, `go tool task race:zarlcode` runs the engine and TUI
race suites, including the event sink. For changes to shared history storage or
the runner, also run the owning `zkit` tests; those are not covered by racing
application packages alone.
