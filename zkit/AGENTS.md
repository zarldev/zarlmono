# AGENTS.md — `zkit`

Shared-library guidance for the `zkit` module. Package-specific instruction files below this directory take precedence.

## Verification

```bash
go test -C zkit -count=1 ./...
go test -C zkit -race ./...
```

Use narrower package checks while iterating, such as `go test -C zkit -count=1 ./agent/runner/...`.

## Module invariants

- `zkit` is the canonical shared implementation for runner, tools, providers, guardrails, compaction, MCP, options, and foundation packages.
- Avoid circular dependencies. `zkit/options` is the universal functional-options dependency.
- `zkit/options.Option[T]` is the canonical options shape.
- Provider registration under `ai/llm` deliberately uses `init()` side effects; do not remove registration imports as unused.
- Constructors return concrete types; interfaces belong to consumers.
- Application composition is trusted internal wiring, not an untrusted boundary. Pass already-created dependencies and parsed domain values directly into constructors.
- Constructors assemble controlled required dependencies; functional options assign optional policy. Do not add nil checks, typed-nil reflection, silent option guards, normalization, or error returns for impossible internal wiring.
- Validation belongs where raw external data enters. Convert raw HTTP/config/storage/message representations to semantic types there, then rely on those types and the controlled call graph inside the module.
- Every goroutine needs an owner and shutdown path.
- Generated enums come from `*_enum.go`/`enums.go`; run `go generate` and never edit generated `*_enums.go` files.
