# Shared inference accounting

`inference.Account` is a host-owned, opt-in aggregate for provider-stream invocations. It is independent of runner attempt events and task-local usage totals. It does not replace a provider, qualify automatic observations, grant tool permissions, or start a background worker.

## Composition

Create one account per goal, then pass `inference.Scope{Account: account, Member: memberID}` explicitly to each inference owner:

- `runner.WithInferenceScope` for ordinary runner attempts;
- `spawn.WithPlannerInferenceScope` for routing inference;
- `guardrails.WithVerdictInferenceScope` for decomposition judgments;
- `Summary`, `Executive`, and `Handover` compactor `InferenceScope` fields.

`engine.NewSwarm` enables shared accounting and propagates each member scope through root activations, recursively constructed children, routing, decomposition judgments and automatic compaction. `InferenceSnapshot` returns goal-wide consumption. Ordinary task/session totals remain unchanged; **do not add the goal aggregate to those totals**.

Standalone `swarm.New` retains direct activation-result accounting unless `swarm.WithInferenceAccounting()` is selected. An opting-in executor must propagate `Activation.InferenceScope()` itself. Unscoped/custom provider calls are outside the account.

## Invocation and settlement

`Scope.Complete` creates a lazy stream: construction does not admit or invoke the provider. `Scope.Invoke` admits, constructs the stream, and passes it synchronously to one consumer; this separation preserves the runner's established stream timing. The consumer must iterate or stop that stream before returning and must not concurrently consume or retain it. `Scope.Wrap` can account an already-constructed lazy stream but cannot retroactively gate its construction.

Admission refusal produces `ErrBudget` without upstream invocation. Runner refusal produces no fake provider attempt event or usage. Every admitted call settles once, at stream return, including errors, early consumer stop and panic unwinding. Panics propagate. Settlement precedes consumer cleanup so normal cleanup cancellation is not misclassified as provider failure.

Each settlement has an account-local ID, host purpose/provider/model labels, logical member and existing task/depth correlation. The last reported usage snapshot is retained; cumulative chunk snapshots are **not** summed. Missing usage is counted as unknown, separately from reported zero. A subsequent planner/judge JSON parsing failure cannot erase the already-settled provider usage.

The optional settlement callback is synchronous, receives value-owned data, and runs after accounting commits outside the mutex. It must tolerate concurrent calls, must not retain the operation context and must not block execution indefinitely. It is telemetry, not an admission decision or durable mailbox.

## Limits

- **Invocations:** atomically denies requests after N provider-stream calls have been admitted. Already-admitted calls may finish. The exhaustion signal closes when they have all settled.
- **Reported tokens:** closes exhaustion as soon as settled reported consumption reaches the threshold; a goal owner may then cancel other work. Parallel in-flight work can overshoot.
- Tokens normalize each final usage snapshot using `max(total, prompt + completion)`. Reasoning tokens are already part of completion and are not added again. Cache fields remain available in the aggregate.
- Provider-internal network retries are part of one provider-stream invocation. Missing provider usage is not estimated. This is not a hard billing cap or pricing ledger.

Account exhaustion only signals the owner. Shared swarm execution cancels and joins its workers/descendants before returning; an account alone cannot stop a provider or close borrowed resources.
