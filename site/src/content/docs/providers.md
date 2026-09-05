---
title: LLM providers
description: One streaming interface, adapters for the providers that matter, a registry that owns names and keys, and template adapters for wire-format quirks.
---

`zkit/ai/llm` is the provider abstraction. The part the runner
cares about is one method:

```go
Complete(ctx context.Context, req CompletionRequest) CompletionStream
```

`CompletionStream` is a named range-over-function stream. Calling `Complete` only constructs a one-shot invocation: request preparation, token refresh, transport/process setup, I/O, and operational failures begin when it is ranged. Consumption is direct and synchronous, with no chunks channel or producer goroutine.

Normal return/EOF is success. A terminal failure is exactly one zero-chunk error yield followed by return; there is no outer error or `Done` chunk. Finish reason and usage are optional ordinary metadata. Request reference-backed state is borrowed until iteration returns, and yielded reference-backed chunk state only until the callback returns, so retaining consumers clone synchronously. Providers propagate cancellation through setup, reads, decoders, and subprocess cleanup.

`runner.ClientFromProvider` narrows a full provider down to exactly this — and the narrowing is the point: new capabilities go on new interfaces, not on the one the loop depends on.

## Adapters

| Adapter | Auth | Notes |
|---|---|---|
| `openai` | API key | Reference implementation; also the base for every OpenAI-compatible endpoint. |
| `anthropic` | API key | Native SDK; retries 429 with backoff. |
| `google` | API key | Gemini; retries the free tier's tight rate limits. |
| `llamacpp` | none (local) | OpenAI adapter pointed at llama-server, with a stream-friendly HTTP client. |
| `ollama` | none (local) | OpenAI adapter pointed at Ollama. |
| `openaicodex` | OAuth | ChatGPT-subscription backend. Retries 429/5xx honouring Retry-After. |
| `claudecode` | OAuth | Claude-subscription backend. |
| `deepseek` | API key | OpenAI-compatible facade pointed at api.deepseek.com. |
Constructors are option-based:

```go
p, err := openai.NewProvider(apiKey, openai.WithModel("gpt-5.5"))
p, err := llamacpp.NewProvider(llamacpp.WithBaseURL("http://127.0.0.1:8081"))
```

## The backends registry

Hand-rolled `switch name { case "openai": … }` blocks rot. The
`backends` package owns the closed set of provider definitions —
canonical name, constructor, API-key requirements, seed model IDs, live
model-list fetcher, context-window and cost metadata — and builds
providers from names:

```go
reg := backends.NewRegistry() // builtins with explicitly supplied configuration
p, err := reg.BuildWithConfig(ctx, "anthropic", backends.BuildConfig{
	Model: "claude-sonnet-4-6",
	APIKey: configuredAPIKey,
})
```

Credentials come from an explicit `BuildConfig.APIKey`, then the credential
source supplied through `WithSettingsService`. There are no environment-variable
fallbacks for API keys or provider URLs. Zarlcode supplies its database-backed
preference service; protected credentials require an explicitly unlocked vault.

`WithStore` adds user-defined providers from your storage;
`WithProviderDefinitions` replaces the builtin seed set. Local backends
(llamacpp, ollama) declare no key requirement.

If you find yourself writing a name→constructor switch outside this
package, the registry already does it.

## Chat-template adapters

Models disagree about the wire format: where the system prompt
lives, how tool calls are framed, which thinking tag applies.
`zkit/ai/llm/templates` normalises this at the boundary so the
runner stays agnostic — adapters for the major local model families'
envelopes and a passthrough for managed APIs that handle templating
server-side. Wire one with `runner.WithTemplate`.

## Provider-side recovery

Failure handling lives where the failure happens:

- **Rate limits** retry inside the adapters with exponential backoff
  and Retry-After honoured — invisible to the runner, which matters
  when a parent task fans out sub-agents that all hit the same
  backend at once.
- **Server-side tool-JSON rejection** (llama.cpp validates tool-call
  arguments before the runner ever sees them) is recovered in the
  runner with a corrective message — see
  [Runner](/zarlmono/runner/#what-makes-it-survive-real-models).
- **Conformance** is enforced by `zkit/ai/llm/providertest`, a
  shared test suite every adapter runs, so "streams content",
  "accumulates tool-call fragments", and "honours ctx cancellation"
  mean the same thing across providers.

## Conformance testing

`zkit/ai/llm/providertest` supplies shared scenarios and canonical assertions for full laziness, successful EOF and terminal errors, cancellation before and during iteration, downstream early stop, usage/finish metadata, and tool-call fragment accumulation. Each adapter runs the scenarios relevant to its wire protocol; provider-local tests cover transport-specific retry, parsing, and cleanup details. Adding an adapter means selecting the shared scenarios deliberately rather than inventing incompatible streaming semantics.