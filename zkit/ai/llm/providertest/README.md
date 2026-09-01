# providertest

`providertest` is the shared conformance harness for `llm.Provider`
implementations. Each backend supplies provider-specific `http.HandlerFunc`
fixtures and a factory pointed at the scenario server; the harness owns the
provider-neutral stream checks.

## Stream contract exercised

The harness follows `llm.CompletionStream` directly:

```go
stream := provider.Complete(ctx, request) // constructs only; no I/O
for chunk, err := range stream {          // work starts here, synchronously
    // chunk data is borrowed for this callback; retain chunk.Clone()
}
```

- `Complete` has no outer error and must remain lazy. The harness verifies that
  no request reaches the stub server before stream invocation.
- Consumption is synchronous in the scenario goroutine. There are no forwarding
  channels, producer goroutines, or stream shims.
- Normal return/EOF is successful completion. No `Done` chunk exists or is
  required.
- A terminal failure is one `(llm.CompletionChunk{}, err)` sequence value,
  immediately followed by return. The harness keeps that error separate instead
  of folding it into a chunk.
- Retained chunks are cloned during the yield callback so assertions own all
  reference-backed data.
- Usage is an owned `llm.Usage` value whose presence is represented by
  `UsageReported`, including legitimate all-zero usage.
- Finish reasons are provider-neutral `llm.FinishReason` values. Assertions
  should test semantic values such as `llm.FinishReasons.STOP`, not wire strings.

## Canonical assertions

- `AssertSuccessfulCompletion` and `AssertStreamingEOF` require successful EOF.
- `AssertCancellationHonoured` requires cancellation as the terminal sequence
  error.
- `AssertUsageReported` requires successful EOF and an observation with
  `UsageReported=true`; zero token counts remain valid.
- `AssertFinishReasonReported` requires a recognized, non-unknown semantic finish
  reason. `AssertFinishReason(want)` checks an exact semantic value.
- `AssertToolCallEmitted(name)` requires the named tool call before successful
  EOF.
- `AssertErrorSurfaced` requires a terminal sequence error; the harness verifies
  its zero-chunk shape and terminal position.
- `AssertStoppedByConsumer` checks clean return after downstream `false`.
- `AssertPreCanceled` checks a pre-canceled context yields no normal chunks and
  one cancellation error.

## Scenario controls

`CancelMidStream`, `PreCancel`, and `StopAfter` are opt-in controls for scenarios that exercise those paths. `CancelMidStream` cancels synchronously after the first accepted normal chunk. `PreCancel` cancels before `Complete`, allowing a scenario to prove no request I/O occurs for an already-canceled invocation. `StopAfter` breaks direct consumption after the requested number of normal chunks, exercising false-yield cleanup. `Timeout` bounds the invocation and provides the context deadline; it is not implemented with a detached consumer goroutine.

## Adopting in a backend

1. Add `conformance_test.go` beside the provider tests.
2. Build the provider in `Suite.Factory`, pointed at the supplied `baseURL`.
3. Supply one wire-faithful handler per scenario.
4. Select the shared controls and canonical assertions that match the adapter's wire protocol, then pass those scenarios to `providertest.Run`.

Provider-specific retry, multimodal encoding, and thinking transport details stay
in provider-local tests. Shared scenarios should assert only the public
provider-neutral contract.
