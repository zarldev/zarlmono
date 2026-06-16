# AGENTS.md

Guidance for working in `zkit/ai/`.

## What this package is

The AI substrate consumers build on: an LLM provider abstraction plus tool execution. Two subpackages, `llm/` and `tools/`. It provides a narrow contract over OpenAI-compatible, Anthropic, Google, and other providers, plus a canonical tool interface with effect metadata and typed error kinds.

## Tree

```
zkit/ai/
├── llm/                  # Provider abstraction + per-backend impls
│   ├── provider.go       # Provider interface, CompletionRequest, streaming contract
│   ├── openai/           # OpenAI SDK + OAI-compatible facade (llama.cpp/vLLM/ollama)
│   ├── anthropic/        # Claude SDK adapter
│   ├── google/           # Gemini (genai SDK), retry-on-429, thinking support
│   ├── deepseek/         # OpenAI-compatible facade → api.deepseek.com
│   ├── llamacpp/         # thin facade over openai/ → localhost:8081/v1
│   ├── ollama/           # thin facade over openai/ → localhost:11434/v1
│   ├── claudecode/       # OAuth-backed claude.ai/code surface
│   ├── openaicodex/      # OAuth-backed ChatGPT surface
│   ├── backends/         # Registry: name → provider build, OAuth gating
│   ├── repair/           # Tool-call JSON recovery for small-model fallout
│   ├── templates/        # Chat-template metadata + thinking-tag helpers
│   └── providertest/     # Conformance harness (Scenario, Suite, assertions)
└── tools/                # Tool execution + registry
    ├── tools.go          # Tool, ToolCall, ToolResult, ToolName, Iterable/Executor/Source
    ├── schema.go         # JSON Schema types + SchemaFor
    ├── signature.go      # CallSignature: canonical (tool, args) hash for dedup/memo
    ├── errors.go         # Error, Kind enum (Validation/NotFound/Permission/Budget/Fatal)
    ├── mcp.go            # Model Context Protocol bridge + RemoteTool wrapper
    ├── code/             # File-mutating tools (write, edit, apply_patch, bash, grep, ls)
    ├── fetch/ search/    # HTTP fetch, SearXNG web search
    ├── dynamic/          # SQLite-backed dynamic tool registration + MCP connection tools
    └── toolkit/          # Typed tool builder + schema generation
```

## Provider interface

Deliberately narrow:

```go
type Provider interface {
    Complete(ctx context.Context, req CompletionRequest) (iter.Seq2[CompletionChunk, error], error)
    Name() string
}
```

Anything richer (model discovery, image gen, OAuth, conversation state) belongs on a separate opt-in interface that consumers type-assert for. Don't widen `Provider`.

## Design rules

Repository style, applied throughout:

- **Errors tell a story.** Wrap at every failure point: `fmt.Errorf("context: %w", err)`. Never "failed to …". Log once at the boundary.
- **Small, emergent interfaces.** Consumer-defined, not design-first. The narrow `Provider` is the model — additions must justify themselves.
- **No fire-and-forget.** Every goroutine has a defined lifecycle. Streaming providers own the channel they emit on and close it on return.
- **Fakes over mocks.** Use `zkit/ai/llm/providertest` for new provider tests — it bundles the conformance harness and canonical assertions.

## Tools

Tool handlers take a typed-parameter struct (`tools.SchemaFor[Args]` + `tools.DecodeArgs[Args]`) — don't reach for `map[string]any` unless the args are genuinely unconstrained. `tools.Error` carries a `Kind` (`Validation`, `NotFound`, `Permission`, `Budget`, `Fatal`); downstream policy (guardrail decomposition, escalation) routes on `Kind`, so tag errors correctly via the constructors. Visibility and execution flow through three interfaces: `Iterable` (enumerate, cheap, no I/O), `Executor` (dispatch a call), and `Source` (both).

## Testing

- Black-box (`package_test`), table-driven with `t.Run`, `t.Context()` not `context.Background()`.
- Use `providertest.Suite` + `providertest.Scenario` for a new provider — it covers cancellation, streaming-done, usage reporting, and tool-call surfacing.

```bash
go test -C zkit ./ai/...
go test -C zkit -race ./ai/...
```

Integration tests requiring real API keys live behind a build tag:

```bash
OPENAI_API_KEY=sk-... go test -C zkit -tags=integration ./ai/llm/openai/
```
