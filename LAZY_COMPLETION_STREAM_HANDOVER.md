# Handover: Lazy iterator-native LLM completion stream migration

## Start here

Load the authoritative implementation plan:

```text
.zarlcode/plans/lazy-completion-stream-migration.md
```

It contains the settled architecture, repository-wide migration inventory, provider and consumer risks, ordered phases, verification gates, and explicit multi-agent ownership/dependency model. Do not reconstruct the design from this summary alone.

## Final status

- The migration is implemented across the core contract, providers, conformance harness, runner, internal consumers, zarlcode, and examples.
- `CompletionStream` is the direct, lazy, synchronous range-over-function contract described below.
- Provider and runner race tests, full `zkit`, examples, and SWE-bench tests pass.
- Anthropic SDK `v1.68.0` and JSON Schema `v0.14.0` are aligned across affected modules.
- Repository-wide verification still reports unrelated pre-existing zarlcode TUI test/lint failures; see the current workspace status rather than treating this handover as a clean-tree claim.
- This file now records the settled architecture and completed migration. The saved plan is historical implementation detail, not current task status.

## Explicit user direction

Breaking changes are acceptable:

> “I DO NOT CARE ABOUT BREAKING CHANGES”

Do not add compatibility adapters, deprecated aliases, transitional overloads, or defensive API preservation.

The user explicitly rejected replacing the iterator with a channel-backed or object-backed stream. That would be a regression.

## Non-negotiable architecture

### The iterator is the stream

Use a named range-over-function type:

```go
type CompletionStream func(func(CompletionChunk, error) bool)
```

It remains directly rangeable:

```go
for chunk, err := range stream {
    // synchronous consumption
}
```

Do not introduce a stream struct, result channel, result envelope, producer goroutine, `Close` method, asynchronous yield, or buffered forwarding. Preserve synchronous backpressure, allocation reuse, borrowed-value lifetime, and downstream `false` propagation.

### Fully lazy provider contract

```go
type Provider interface {
    Complete(context.Context, CompletionRequest) CompletionStream
    Name() string
}
```

Calling `Complete` performs no I/O, token refresh, process start, transport acquisition, goroutine start, response-body acquisition, or external side effect. Execution and all operational failures begin only when the sequence is invoked or ranged.

### Terminal semantics

Remove `CompletionChunk.Done` and `CompletionChunk.Error`.

- Normal sequence return means successful completion.
- Terminal failure is exactly one `yield(CompletionChunk{}, err)`, followed immediately by return.
- No yields occur after an error.
- If downstream `yield` returns `false`, clean up and return silently.
- Do not emit cancellation or another terminal event after downstream stops.

Usage and finish metadata may be emitted as ordinary optional metadata-only chunks. They are not lifecycle sentinels and must not be used to infer completion. A metadata-only chunk must contain meaningful metadata. Successful completion does not require metadata.

### Borrowed ownership

`CompletionRequest` reference-backed state is borrowed until iteration returns. Yielded reference-backed chunk state is borrowed only until downstream `yield` returns.

- Providers and middleware must not mutate borrowed request state.
- Adapters clone only structures they actually need to change.
- Middleware forwards synchronously.
- Retaining consumers clone before returning from `yield`.
- No queued shallow chunks.
- Audit runner usage and tool-call retention carefully.

Add `CompletionChunk.Clone` only if it centralizes real retention boundaries without burdening allocation-free consumers.

### Usage and finish reason

Planned chunk shape:

```go
type CompletionChunk struct {
    Content       string
    Thinking      string
    ToolCalls     []ToolCall
    FinishReason  FinishReason
    Usage         Usage
    UsageReported bool
}
```

Usage is an owned value with explicit presence, avoiding pointer aliasing while preserving legitimate all-zero reports.

Add a semantic generated `FinishReason` enum using the repository’s `goenums` workflow. Normalize provider wire values at adapter boundaries. Unknown values map to `Unknown`; raw values may be retained in tracing but must not drive runner policy. Never edit generated `*_enums.go` files manually.

### Iterator-preserving middleware

```go
type CompletionMiddleware interface {
    Wrap(CompletionStream) CompletionStream
}
```

Recommended ordering:

```go
base.With(A, B) == A.Wrap(B.Wrap(base))
```

Every wrapper invokes upstream and downstream synchronously, returns downstream’s boolean unchanged, stops immediately after `false`, never buffers or asynchronously yields, never retains borrowed chunks, and never starts a chunk-forwarding goroutine.

Metrics, tracing, idle observation, and safe retry policy belong in these wrappers.

### Metrics

Metrics wrappers derive owned scalar summaries—duration, time to first accepted event, provider wait, consumer time, event/byte/tool-call counts, usage presence, finish reason, outcome, and cancellation cause—and record exactly once after wrapped iteration returns.

If export is asynchronous, the recorder owns its queue and shutdown lifecycle. It receives owned summaries, never borrowed chunks.

### Idle timeout and cancellation

Remove the buffered producer channel in `zkit/agent/runner/drain.go`. The runner directly consumes the decorated sequence.

Idle time is time controlled by upstream without offering a semantic event. It includes setup, time to first event, and gaps between events. It excludes all time inside downstream `yield`.

A watchdog goroutine is acceptable only with one invocation owner, explicit stop condition, explicit join path, and no chunk transport/retention.

Use context causes:

- Iteration deadline → `ErrIterationTimeout`.
- Idle deadline → `ErrStreamIdle`.
- Caller cancellation preserves caller cause.

Classify with `context.Cause`, not elapsed-time reconstruction.

Every provider must connect cancellation to token acquisition, HTTP/SDK setup, stream reads, decoder loops, and subprocess lifecycle. A provider that ignores cancellation violates the contract. Do not reintroduce a producer goroutine and leak it to accommodate broken provider code.

### Retry

Retry only on a retryable terminal error before any event was accepted by downstream. Accepted means downstream returned `true`, including empty or metadata events. Never retry after an accepted event, after downstream returns false, or for caller/iteration/idle cancellation. Audit Google’s internal retry separately from runner retry.

## Guidance status

The active provider, LLM, runner, package, site, and conformance guidance now describes the lazy synchronous protocol. Historical references below identify the migration surface; they are not outstanding work.

## Migration surface

Providers:

- `zkit/ai/llm/openai`
- `zkit/ai/llm/anthropic`
- `zkit/ai/llm/google`
- `zkit/ai/llm/openaicodex`
- `zkit/ai/llm/claudecode`
- `zkit/ai/llm/deepseek`
- `zkit/ai/llm/llamacpp`
- `zkit/ai/llm/ollama`
- `zkit/ai/llm/named.go`

Consumers include runner, compact summary/executive/handover, decompose judge, spawn planner, computer-use examples, provider/client test doubles, zarlcode engine tests/inspector, and possible swebench call sites.

The initial inventory found 56 files containing completion declarations/calls and many test doubles using the old raw `iter.Seq2` plus outer-error signature. Re-run the inventory because the workspace may have changed.

## Multi-agent execution

The saved plan defines detailed disjoint ownership and integration gates.

### Wave 0 — lead/orchestrator

Load plan/guidance, capture status/diffs, re-run inventory, establish baseline, and assign disjoint paths. No overlapping implementation ownership.

### Wave 1 — core protocol owner

Owns `zkit/ai/llm/provider.go`, `doc.go`, new stream/middleware foundation, finish-reason enum source/generated output, and core external protocol tests. Follow with a read-only contract review.

### Wave 2 — parallel providers

- Agent A: OpenAI, DeepSeek, llama.cpp, Ollama.
- Agent B: Anthropic, Google.
- Agent C: OpenAI Codex, Claude Code.
- Optional Agent D: `named.go` and unassigned neutral facades.

Integrate and pass the provider gate before consumer work.

### Wave 3 — parallel consumers/conformance

- Agent E: runner, direct consumption, idle lifecycle, context causes, runner-owned metrics interfaces.
- Agent F: compact, guardrails judge, spawn planner.
- Agent G: zarlcode, examples, swebench, assigned snippets.
- Agent H: `zkit/ai/llm/providertest` and shared conformance fixtures.

Integrate and pass consumer/concurrency gates.

### Wave 4

Update guidance/docs and run repository-wide cleanup searches. Return findings to original path owners.

### Wave 5

Lead owns formatting, generation verification, focused tests, race tests, full module checks, cross-module checks, root check/lint, and final independent review.

## Workspace precautions

Before assigning or editing paths:

```bash
git status --short
git diff -- <assigned-paths>
```

- Preserve all user work.
- Never use `git restore`, `git checkout --`, or `git revert`.
- Re-read files immediately before editing.
- Agents may inspect outside assigned paths but mutate only assigned files.
- Do not opportunistically fix unrelated code.
- Lead resolves integration.

Existing uncommitted work includes changes around Gemini finish reasons, MCP error context, notification retention, and zhttp documentation. Do not overwrite them or assume all existing diffs belong to this migration.

## Verification

Focused:

```bash
go test -C zkit -count=1 ./ai/llm/...
go test -C zkit -race -count=1 ./ai/llm/...
go test -C zkit -count=1 ./agent/runner ./agent/compact ./agent/guardrails ./agent/tools/spawn
go test -C zkit -race -count=1 ./agent/runner
```

Module and cross-module:

```bash
go test -C zkit -count=1 ./...
go test -C zkit -race ./...
go vet -C zkit ./...
go test -C examples -count=1 ./...
go test -C zarlcode -count=1 ./...
go test -C swebench-eval -count=1 ./...
go tool task check
go tool task lint
```

Integrity:

```bash
gofmt -l zkit zarlcode examples swebench-eval
git diff --check
golangci-lint config verify
```

Keep `github.com/anthropics/anthropic-sdk-go` and
`github.com/invopop/jsonschema` aligned to the validated `v1.68.0`/`v0.14.0`
pair.

## Completed acceptance criteria

All providers use lazy `CompletionStream`; `Complete` performs no work before iteration; no outer completion error, completion `Done`, or chunk-level error remains; no producer channel or tolerated goroutine leak remains; middleware preserves synchronous iterator semantics; borrowed values are not retained without copying; providers honor cancellation; finish reasons and usage presence are semantic/owned; and active guidance matches the implemented protocol. Verification results and unrelated workspace blockers are tracked separately from these completed architectural criteria.
