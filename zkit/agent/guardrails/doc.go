// Package guardrails provides policy wrappers around agent tool execution.
//
// Guardrails are small checks that run before or after a tool call. PreCall
// guardrails can reject malformed, unsafe, over-budget, repeated, or otherwise
// disallowed calls before the underlying tool executes. PostCall guardrails can
// inspect results, reclassify failures, validate output shape, or enforce
// task-level invariants after execution.
//
// GuardedSource composes guardrails around a tools.Source without changing the
// tool list exposed to the model. Rejections are converted into failed tool
// results so the runner can append them to the conversation and let the model
// react, rather than surfacing them as hard dispatch errors.
//
// Guardrails may run concurrently because runners can dispatch multiple tool
// calls in parallel. Any guardrail that stores mutable per-task state must
// synchronize its own state and implement lifecycle cleanup where needed.
package guardrails
