# AGENTS.md

Guidance for working in `zkit/ai/llm/`.

## What this package is

The provider-neutral language-model contract: the narrow `Provider` interface consumers target, plus per-backend implementations. No business logic — purely the wire-level adapter layer.

## Provider interface

```go
type Provider interface {
    Complete(ctx context.Context, req CompletionRequest) (iter.Seq2[CompletionChunk, error], error)
    Name() string
}
```

That's the whole contract. Anything richer (model discovery, image generation, OAuth refresh) lives on a separate opt-in interface that consumers type-assert for. Don't widen `Provider`.

## Streaming contract

Every provider's `Complete` MUST:

1. Return an `iter.Seq2[CompletionChunk, error]` and own the goroutine backing it.
2. Emit chunk errors on the sequence's second value (`for chunk, err := range seq`), not as a chunk field.
3. Emit **exactly one** terminal chunk with `Done: true`. On success it carries final `Usage` and `FinishReason`; on error it carries the stream error plus `Done: true` — never an error without `Done`, because consumers key off `Done` for completion.
4. Honour context cancellation: every blocking call inside the goroutine selects on `ctx.Done()` or is an SDK call that propagates context.

`openai/provider.go` is the reference shape.

## CompletionRequest

Carries `Messages`, `Temperature`, `MaxTokens`, `Stream`, `Tools`, plus four escape hatches:

- **`ChatTemplateKwargs`** — wire-level extension for llama.cpp / vLLM (`chat_template_kwargs`); built from the chat template's thinking kwargs for Qwen3-style loops. Providers that don't recognize it drop it.
- **`ResponseFormat`** — pins output shape via JSON Schema. On OpenAI this hits structured output; on llama.cpp it triggers GBNF-constrained sampling (the model literally cannot emit a token sequence violating the schema, including invented enum values). Anthropic and Google ignore this field.
- **`Thinking`** — toggles extended reasoning, mapped to each provider's native mechanism (Anthropic budget, Gemini config, OpenAI/codex effort, llama.cpp kwargs). Providers that surface reasoning unconditionally ignore the toggle.
- **`Options ModelOptions`** — a `map[string]any` escape hatch. Reach for it only when a feature isn't worth a typed field yet; typed fields graduate when a second consumer wants the same thing.

## Message content

Messages carry `Content` (visible answer) and separate `ReasoningContent` (chain-of-thought). The separation is the provider contract — Anthropic extended thinking, DeepSeek/OpenAI `reasoning_content`, Gemini thought parts, and codex reasoning events all land in `ReasoningContent`, out of band from `Content`. The runner populates it from `CompletionChunk.Thinking` at end-of-turn; per-provider history serializers reshape it back onto the wire. Messages also carry `Parts` (multimodal text/image/audio), `ToolCalls`, and `ToolCallID`.

## Provider tree

| Provider | Type | Notes |
|---|---|---|
| `openai/` | core | OpenAI SDK; the OAI-compat surface every "OpenAI-like" provider rides on |
| `anthropic/` | core | Claude SDK; `cache_control: ephemeral` on system prompts for prompt-cache hits |
| `google/` | core | genai SDK; retry-on-429 honouring Retry-After; synthesises `<think>` from thought parts |
| `llamacpp/` | facade | thin facade over `openai/`; defaults to `localhost:8081/v1`, no total-request timeout (local generations can be long) |
| `ollama/` | facade | thin facade over `openai/`; defaults to `localhost:11434/v1` |
| `deepseek/` | facade | facade over `openai/` → `api.deepseek.com`; handles reasoning-history differences |
| `claudecode/` | OAuth | claude.ai/code surface; built from `claudecode.NewProvider(tokenSource, opts...)` |
| `openaicodex/` | OAuth | ChatGPT/Codex surface; built from `openaicodex.NewProvider(tokenSource, opts...)` |
| `backends/` | registry | `Parse` + `Build` for static configs; returns `ErrOAuthRequired` for OAuth backends |
| `repair/` | utility | JSON-recovery cascade for small-model tool-call argument fallout |
| `templates/` | utility | chat-template metadata + thinking-tag split |
| `providertest/` | harness | `Scenario` + `Suite` + canonical assertions for provider conformance |

llama.cpp is the **default zarlcode provider** when no provider is configured.

## Adding a new provider

1. New directory `llm/<name>/`.
2. Implement `llm.Provider`, following the streaming contract.
3. Add the `LLMProvider` constant + parse case.
4. Wire into `backends/registry.go` (a `buildX` function + the `Parse` switch). If OAuth, return `ErrOAuthRequired` and expose a typed `NewProvider(tokenSource, opts...)` callers use directly — see `claudecode/`, `openaicodex/`.
5. Add `<name>/conformance_test.go` using `providertest.Suite` — the four canonical scenarios (cancellation, streaming-done, usage-on-final, tool-calls-surfaced) are the baseline.

If the new backend is OAI-compatible, prefer a thin facade over `openai.NewProvider` (see `llamacpp/`, `ollama/`) — wire-format extensions slot in via options, not a separate streaming implementation.

## Conventions

- **Error wrapping:** wrap at every failure point (`fmt.Errorf("stream: %w", err)`), never "failed to …". Boundary sentinels go in `provider.go` (`ErrProviderUnavailable`, `ErrInvalidAPIKey`, …); provider-internal sentinels live in each subpackage.
- **Testing:** black-box (`package openai_test`); the providertest harness for new providers; integration tests behind `//go:build integration`; byte-level wire-shape checks in subpackage-local `_test.go` files.

```bash
go test -C zkit ./ai/llm/...
go test -C zkit -race ./ai/llm/...
OPENAI_API_KEY=sk-... go test -C zkit -tags=integration ./ai/llm/openai/
```
