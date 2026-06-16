# providertest

Shared conformance harness for every `llm.Provider` implementation.

Each backend speaks its own wire shape — OpenAI's `chat/completions`
streaming chunks look nothing like Codex's `response.*` events, and
both differ from Anthropic's `content_block_delta` blocks — so a
single stub server can't speak them all. The harness instead asks
each backend to supply per-scenario `http.HandlerFunc`s that emit
the bytes the backend expects, plus a `Factory` that wires a real
Provider against the stub URL. The assertions on top of the
resulting chunks are uniform.

## What gets tested

The shipped assertions cover the contracts the runner relies on:

- **`AssertCancellationHonoured`** — when `ctx.Done` fires the
  provider closes the chunks channel and emits a cancellation
  marker (chunk.Error, completeErr, or just a clean close — any of
  the three is acceptable).
- **`AssertStreamingDoneSet`** — the final non-error chunk has
  `Done: true`. The runner's loop exit reads this signal; a
  provider that closes the channel without `Done` violates the
  contract.
- **`AssertUsageReported`** — when the server emits token counts,
  the final chunk (or one of the last few) carries `Usage` with
  at least one non-zero field. The runner's token-pressure
  compaction policy depends on this.
- **`AssertToolCallEmitted(name)`** — at least one chunk surfaced a
  `ToolCall` for the named function. Use with the
  `RequestWithTool` helper to advertise a tool.
- **`AssertErrorSurfaced`** — when the server returns 4xx/5xx, the
  provider indicates failure (either via `completeErr` or a chunk
  with `Error` set). Silent success on a server error is a contract
  violation.

Add new assertions in `asserts.go` when the contract grows. The
naming convention is `Assert<ContractName>` returning the
`Scenario.Assert` signature so backends can drop them straight in.

## Adopting in a backend

1. Add a `conformance_test.go` next to the backend's other tests.
2. Write a `Factory` that builds your `llm.Provider` pointed at the
   supplied `baseURL`. Any handshake (OAuth token swap, vault
   lookup) belongs here.
3. For each Scenario, hand-roll a `http.HandlerFunc` that emits the
   appropriate wire bytes for that scenario. Mirror the actual
   wire format your backend speaks to a real server — that's the
   half that has to be backend-specific.
4. Call `providertest.Run(t, providertest.Suite{Factory, Scenarios})`.

The OpenAI (`zkit/ai/llm/openai/conformance_test.go`) and Codex
(`zkit/ai/llm/openaicodex/conformance_test.go`) tests are the
reference implementations.

## What's deliberately NOT covered

- **Retry behaviour.** Per-provider — some go through `zhttp.Client`
  with retry built in, some have their own retry loops (Google's
  Gemini), some have none. The right place to test retry is in the
  provider's own tests against its specific retry knobs.
- **Multimodal / vision.** Each provider invented its own multipart
  representation; the runner only cares that text-only conversations
  round-trip. Vision providers test their own `Parts` handling.
- **Reasoning / thinking-mode.** Same — every provider tagged
  `<think>` differently and ChatTemplateKwargs is a llama.cpp /
  vLLM thing only. Provider-specific tests stay in the provider's
  package.

These could be added as Scenarios later if the contract grows to
include them; today they're explicitly out of scope so the harness
stays simple enough that backends actually adopt it.

## Why per-scenario stub handlers, not a single backend mock

We tried a single stub server speaking a "common" protocol and
the per-provider conformance test wrapping a translation layer.
It collapsed into "the test for translation layer X" — and
translation layer X had its own bugs, so a green run didn't tell
you the provider itself was healthy.

The current shape keeps the wire-shape stub close to the provider
it tests. A bug in the OpenAI provider's SSE parser fails the
OpenAI conformance run; a bug in the Codex provider's
function_call_arguments handling fails the Codex run. No
translation-layer middle to confuse the signal.
