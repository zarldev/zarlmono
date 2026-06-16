# releasegate

A worked example of the **guarded runner + harness** pattern using a real LLM by
default. Unlike `examples/hnupvote`, this one performs no browser automation and
does not touch an external site: the "world" is an in-memory release object, but
the agent loop is real provider → runner → tools → guardrails → pursue.

The concrete task: publish a release to production, but only after a release gate
is green.

The docs/test path also includes an explicit `-scripted` mode. That scripted
client intentionally makes mistakes:

1. Tries to publish immediately.
2. Omits a required JSON field.
3. Writes weak release notes.
4. Fixes the checks and notes.
5. Publishes once the gate allows it.

The harness decides success by checking the `Release` world state, not by trusting
any model claim.

## Running it

Default mode uses a real provider. Pick one and set the matching key:

```sh
# OpenAI (default)
export OPENAI_API_KEY=sk-...
go run ./examples/releasegate -provider openai -model gpt-4o-mini

# Anthropic
export ANTHROPIC_API_KEY=sk-ant-...
go run ./examples/releasegate -provider anthropic -model claude-sonnet-4-6

# DeepSeek
export DEEPSEEK_API_KEY=...
go run ./examples/releasegate -provider deepseek -model deepseek-chat

# Local providers; no API key required
go run ./examples/releasegate -provider ollama -model qwen3:8b
go run ./examples/releasegate -provider llamacpp -base-url http://localhost:8081/v1 -model local-model
```

Useful env vars:

| Env | Meaning |
|---|---|
| `LLM_PROVIDER` | Default provider when `-provider` is omitted. |
| `LLM_MODEL` | Default model when `-model` is omitted. |
| `LLM_BASE_URL` | Generic base URL override. |
| `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GEMINI_API_KEY`, `DEEPSEEK_API_KEY` | Provider-specific keys. |
| `OLLAMA_BASE_URL`, `LLAMACPP_BASE_URL`, `DEEPSEEK_BASE_URL` | Provider-specific endpoint overrides. |

Deterministic no-LLM mode remains available for docs and tests:

```sh
go run ./examples/releasegate -scripted
```

Scripted-mode trace:

```text
  → release_publish
  ✗ release_publish: guardrail "release_ready": ... missing: tests, changelog, rollback_plan, approved_release_notes
  → release_set_check
  ✗ release_set_check: guardrail "schema": ... missing required field "evidence"
  → release_set_check
  ✓ release_set_check
  → release_write_notes
  ✗ release_write_notes: guardrail "release_notes_quality": ...
  ...
  → release_publish
  ✓ release_publish
status=succeeded attempts=1 provider=scripted model="" version=v1.2.3 published=true channel="production" notes_approved=true
```

## What it demonstrates

| Concept | File | What to look for |
|---|---|---|
| World state | `release.go` | Small mutex-protected state object shared by tools, guardrails, and oracle. |
| JSON tools | `tools.go` | Tool schemas with `required`, `enum`, and `additionalProperties:false`; typed argument decode via `tools.DecodeArgs`. |
| Pre-call guardrail | `guardrails.go` | `releaseReadyGuardrail.Before` blocks `release_publish` until the gate is complete. |
| Post-call guardrail | `guardrails.go` | `notesQualityGuardrail.Inspect` rewrites weak notes into actionable tool feedback. |
| Runner wiring | `harness.go` | Registry → `GuardedSource` → `runner.New` with prompt, sink, progress updater. |
| Harness oracle | `harness.go` | Goal checks `Release.Published && channel == production`, not the model's text. |
| Real provider wiring | `provider.go` | Env/flag-backed provider construction for OpenAI, Anthropic, Gemini, DeepSeek, llama.cpp, and Ollama. |
| Deterministic client | `scripted.go` | `-scripted` feeds canned tool calls into the real runner for repeatable tests. |
| Tests | `releasegate_test.go` | End-to-end and focused guardrail tests with in-memory fakes only. |

## The pattern

The reusable shape is the same as `hnupvote`, but safer to run in docs and CI:

```text
pursue.Drive
  └── runner.Run
        ├── real runner.Client from an llm.Provider (or explicit scripted test client)
        ├── tools.Registry
        │     └── guardrails.GuardedSource
        │           ├── schema            (JSON argument validation)
        │           ├── release_ready     (pre-call publish policy)
        │           └── notes_quality     (post-call result rewrite)
        └── EventSink                     (progress trace)
```

The important design point: swapping between OpenAI, Anthropic, Gemini,
DeepSeek, llama.cpp, Ollama, or the deterministic scripted client changes only
the `runner.Client`. Everything else — the tools, guardrails, runner wiring, and
harness oracle — stays the same.

## Testing

```sh
go test ./examples/releasegate/
```

The tests cover:

- the full release flow from failed publish to successful production publish,
- pre-call policy rejection (`release_ready`),
- JSON schema rejection before tool dispatch,
- post-call note-quality rewrite and approval.
