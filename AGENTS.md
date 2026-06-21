# AGENTS.md

Essential, high-signal context for agents working in this repo. If it doesn't help an agent avoid a mistake or ramp up, it doesn't belong here.

---

## Monorepo structure

**Six Go modules** joined by `go.work` (`. ./examples ./swebench-eval ./zarlai ./zarlcode ./zkit`):

| Path | Module path | What it is |
|---|---|---|
| `zkit/` | `github.com/zarldev/zarlmono/zkit` | the canonical shared library (agent, ai/llm, ai/tools, mcp, options, and the foundation packages). |
| `zarlcode/` | `github.com/zarldev/zarlmono/zarlcode` | the zarlcode TUI coding agent, built on `zkit`. |
| `zarlai/` | `github.com/zarldev/zarlmono/zarlai` | smart-home/assistant app, built on `zkit`. CGO deps (go-face/dlib, sherpa-onnx) mean it's **excluded from the standard CI matrix** — it has its own limited CI job. |
| `swebench-eval/` | `github.com/zarldev/zarlmono/swebench-eval` | SWE-bench eval driver; its own module to keep parquet-go out of the root's dependency graph. |
| `examples/` | `github.com/zarldev/zarlmono/examples` | small runnable harnesses, each isolating one `zkit` pattern. |
| `.` | `github.com/zarldev/zarlmono` | root module: repository tooling and workspace coordination (no product packages). |

- **`./...` does NOT cross module boundaries.** In a `go.work` repo, `go build/vet/test ./...` from any directory only expands within the *current* module. To act on everything you must iterate modules (`cd <mod> && go ... ./...`) — see CI.
- **`replace` directives are stripped from tagged modules.** `go.work` handles local resolution; the per-module `go.mod` files must NOT contain `replace` directives pointing to local paths, because the Go module proxy rejects them for `go install`. The release pipeline pins internal deps to published versions.- **Canonical packages live in `zkit/`.**
- **`zkit/options`** is the canonical functional options pattern (`Option[T] func(*T)`). Every package uses it.
- **Package-local agent notes are named `AGENTS.md`.**

## Building and testing

```bash
# zarlcode (the TUI) — lives in its own module
go tool task zarlcode              # build + install to ~/.local/bin/zarlcode (version-stamped)
go run ./zarlcode/cmd              # run from source
go run ./zarlcode/cmd -continue    # resume last session

# Whole repo (mirrors CI; ./... alone would miss the other modules)
go tool task check
# zarlai is omitted above — it needs CGO system libs (dlib, sherpa-onnx) not present in CI.
```

## Code style and conventions

- **golangci-lint:** the root `.golangci.yaml` is the reference config (v2, strict set). CI runs it **per module**. Verify the config against new linter releases with `golangci-lint config verify`.
- **Provider registration uses `init()`** in `zkit/ai/llm/...` backends. Don't remove side-effect imports or registration calls — that's the wire-up mechanism.
- **`zkit/` packages have no circular dependencies** on each other (except `zkit/options`, which is universal).
- Tests live in `_test.go` files alongside source.
- **Godoc convention:** every exported symbol is documented, including interface-implementation methods. An impl method's doc states the implementation-specific behaviour (caps, side effects, semantics of this concrete implementation), never generic boilerplate like "implements tools.Tool". Self-naming const/error groups get one block comment, not per-symbol noise.
- **Pinned dep — `github.com/invopop/jsonschema` MUST stay at `v0.13.0`** in every module. v0.14 swapped its ordered-map type and won't compile against `anthropic-sdk-go`'s `schemautil`. A bare `go get -u ./...` silently re-bumps it; after any `-u`, run `go get github.com/invopop/jsonschema@v0.13.0 && go mod tidy`. (Documented here rather than in go.mod because `go mod tidy` rewrites the indirect block and drops the comment.)

## Things to never do

- **Never `git checkout --` or `git revert` files without explicit user confirmation.** Working-tree changes that look out-of-scope may be the user's parallel work-in-progress. If you see unexpected modifications, ask before reverting.
