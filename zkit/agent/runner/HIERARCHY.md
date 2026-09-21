# Conversation and execution identity

A session owns durable history and usage. Rewind creates a new session sharing an
immutable prefix, not a copy of mutable latest-output projections. A submitted
turn owns a root runner invocation and its drained child-task group.

`ConversationStarted` and `ConversationEnded` bookend **one `Runner.Run`**, not a
session. Child receipts identify the child task itself. `Depth` is recursion
metadata, not parent identity. Applications allocate a fresh task ID per run;
library callers must not mistake an externally reused task ID for a durable
invocation identity. Provider attempts have task-local ordinals distinct from
loop completion counts and storage request generations.

Provider `ToolCallID` values can repeat. `ExecutionID` identifies an occurrence;
`ParentExecutionID` links nested executions and initiating child tasks. An ID
alone does not prove dispatch: interrupted observations can be recorded without
execution. Parallel completion order is not declared sibling sequence order.

## Separate authorities

| Representation | Authority |
| --- | --- |
| Replay occurrence chain | Immutable captured model observations and non-model execution records, shared by branches |
| Working `[]llm.Message` / `TaskResult.Messages` | Mutable, compacted/truncated model context |
| Prepared request | Exact runner-level input snapshot, not provider wire traffic |
| Human transcript | Semantic display hierarchy (`Entry.ID` / `ParentID`) |
| Full-output audit table | Session-owned inspection projection, not branch ancestry |
| Latest-per-call output | Compatibility lookup only |

Non-model executions and interrupted observations never enter rebuilt model
context. Human text never reconstructs provider input. Canonical full tool output
may differ from the truncated text originally sent to a provider. Compaction and
request shaping do not rewrite replay.

Retained chunks, parameters, parts, effects and native continuation data are owned
snapshots. Raw argument bytes remain distinct from repaired execution parameters.
Partial failure output is preserved independently of error presentation. Raw
arguments and outputs belong in explicit inspection, not ordinary diagnostics.

## Usage

Each invoked provider attempt contributes its final reported usage snapshot once,
including errors, retries, cancellation and timeout. Cumulative chunks within one
attempt are not separate spend. Absent usage is nil; reported zero is non-nil.
`LastUsage` is occupancy-oriented; `TotalUsage` is consumption-oriented. A task's
total excludes descendants. Session usage sums root task totals and each child
task's own total once, not once per spawn/status/await result delivery.

Session turns count settled root invocations, not just successful answers.
Same-session resume restores its own spend; a new rewind branch does not charge
inherited history again. Cost is an estimate using the task's model/price basis;
unknown usage or pricing cannot be invented. This is not a crash-complete billing
ledger.

## Capture lifecycle

The application history sink first owns serialized batches in memory. Request
capture commits pending batches before invoking the provider. Terminal full-save
settlement commits and acknowledges exact batch receipts; retry retains the same
IDs and bytes. A successful append callback is not necessarily a disk commit.
Failure stops further provider requests, but cannot roll back external tool
side effects. A crash before commit may lose a pending suffix.

Nested outcomes are appended in serialized capture order, with declared sequence
retained separately. Their parent result can settle later. Child private model
conversations remain excluded from root replay. Turn-owned children and save/event
paths drain before branch activation or resource release.

New readers accept legacy omitted metadata without fabricating identities. The
strict codec rejects unsupported new formats; old binaries are not promised
readability after new fields are written. Immutable old values are never rewritten
to insert defaults. Reachability maintenance must preserve every session,
checkpoint, model-context and direct state-value root.
