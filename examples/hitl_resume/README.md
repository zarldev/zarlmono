# HITL checkpoint resume (no LLM)

A small deterministic example of a real human-review boundary using
`agent/workflow`, `agent/checkpoint`, and `agent/hitl`.

```sh
# Stops at the review boundary and waits for a decision on stdin.
go run -C examples ./hitl_resume

# Non-interactive decisions for scripts and tests.
go run -C examples ./hitl_resume -decision=approve
go run -C examples ./hitl_resume -decision=deny
go run -C examples ./hitl_resume -decision=edit
```

The first workflow prepares a production deployment, saves application-owned
state in a checkpoint, and returns a high-risk `hitl.Request`. `ApproveLowRisk`
leaves that request undecided, so the application pauses at the boundary.

With no `-decision` flag, the example then waits on stdin for an explicit human
`hitl.Review` (`approve`, `deny`, or `edit`). The flag bypasses the prompt for
non-interactive runs. After a decision, the application loads the checkpoint and
calls `continueAfterReview`. That continuation
is deliberately application-defined: **zkit workflow does not automatically
resume a graph from a checkpoint**. A durable application would persist the
checkpoint and review in its own stores and invoke its continuation in a later
process or request.

```sh
go test -C examples ./hitl_resume
```

The external black-box tests run all three flag-driven decisions and an
interactive stdin decision, checking the pause, checkpoint load, and resulting
continuation behavior.
