# healthcheck

A worked example of the **guarded runner + harness** pattern applied to
infrastructure monitoring: check health across multiple endpoints, handle
transient failures via auto-retry, and verify the farm is fully healthy with
a programmatic oracle — not by trusting the model's claim.

The "world" is an in-memory server farm. No network, no external dependencies.
But the agent loop is real: provider → runner → tools → guardrails → harness.

## What it demonstrates

| Concept | File | What to look for |
|---|---|---|
| World state | `world.go` | Endpoints start "unknown"; `Check` auto-promotes on first call. Transient auto-resolves after one check. |
| Tool | `tools.go` | `check_endpoint` with typed args via `tools.SchemaFor` + `tools.DecodeArgs`. Returns `KindTransient` for transient failures so the model can retry. |
| Schema guardrail | `harness.go` | `guardrails.NewSchemaGuardrail(reg)` validates endpoint names before dispatch — the model can't call with a typo'd name. |
| Fanout guardrail | `harness.go` | `guardrails.NewFanoutGuardrail(...)` caps `check_endpoint` at 5 calls per task. Once exhausted, the model gets a spawn_agent nudge. |
| Harness oracle | `harness.go` | Goal checks `farm.AllHealthy()`, not the model's text. `pursue.UntilFunc` provides goal-backed early stop the instant all endpoints are healthy. |
| Deterministic test | `healthcheck_test.go` | Four scenarios: all-healthy, transient-resolves, down-requires-rerive, fanout-caps-excessive. No LLM, no network. |

## The pattern

```
pursue.Drive
  └── runner.Run
        ├── runnertest.Client (or real llm.Provider)
        ├── tools.Registry → check_endpoint
        │     └── guardrails.GuardedSource
        │           ├── schema           (JSON argument validation)
        │           └── fanout           (per-task call cap)
        └── EventSink                   (default stderr progress)
```

## Running

```sh
# Deterministic scripted mode (no LLM, no API key)
go run ./examples/healthcheck -scripted

# Real provider
export OPENAI_API_KEY=sk-...
go run ./examples/healthcheck -provider openai -model gpt-4o-mini
```

## Testing

```sh
go test ./examples/healthcheck/
```

## Key design points

- **Endpoints start unknown.** The oracle can't succeed before the model
  checks anything — `AllHealthy()` is false until every endpoint has been
  checked at least once.

- **Transient auto-resolves.** The farm promotes transient→healthy on the
  first check, so a retry always succeeds. The model sees the transient
  failure, retries, and gets healthy back. No custom retry guardrail needed.

- **Fanout guardrail prevents runaway fan-out.** If the model tries to
  check endpoints one-by-one past the cap, the guardrail blocks with a
  spawn_agent nudge. The model learns to batch or delegate.
