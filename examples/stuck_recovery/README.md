# stuck_recovery

A worked example of **graduated degradation** using `DecomposeGuardrail`. The agent gets stuck in a retry loop; the guardrail tracks the failure pattern and escalates from silent pass-through → advisory hint → fatal error.

## The Scenario

**Task**: "Find the function `NonExistentHandler` in the codebase"

The codebase doesn't contain this function. The agent will:
1. Search for it (fail)
2. Try a different search strategy (fail)
3. Try yet another approach (fail)
4. **Guardrail activates**: suggests spawning a researcher agent
5. Try one more time (fail)
6. **Guardrail blocks**: fatal error, task cannot proceed

## What It Demonstrates

| Concept | File | What to Look For |
|---------|------|------------------|
| Failure tracking | `guardrails.go` | `decomposeBucket` counts failures per signature |
| Graduated response | `guardrails.go` | 1-2 pass, 3 advise, 4 fatal |
| Verdict judge | `guardrails.go` | LLM shapes the advisory message |
| Spawn recommendation | `guardrails.go` | Advisory suggests `spawn_agent` with specific agent |
| Harness retry | `harness.go` | Parent re-drives with feedback after fatal |
| World verification | `harness.go` | Oracle confirms function really doesn't exist |

## Architecture

```
Runner with DecomposeGuardrail
  └── grep tool (keeps failing with "not found")
        ├── Attempt 1: Pass through (silent)
        ├── Attempt 2: Pass through (silent)
        ├── Attempt 3: Advisory (suggests researcher)
        └── Attempt 4: Fatal (guardrail blocks)
              └── Harness re-drives with feedback
                    └── Task succeeds (verified not found)
```

## The Graduated Response

```
Failure count │ Action          │ Model sees
──────────────┼─────────────────┼─────────────────────────────
1             │ Pass through    │ (normal tool failure)
2             │ Pass through    │ (normal tool failure)
3             │ Advisory        │ "Consider using spawn_agent 
              │                 │  with researcher agent to do
              │                 │  a broader codebase search"
4             │ Fatal           │ (guardrail blocks execution)
              │                 │ Harness retries with feedback
```

## Running

```sh
# Deterministic scripted mode (no LLM)
go run ./examples/stuck_recovery -scripted

# Real provider (shows actual LLM-based verdict)
go run ./examples/stuck_recovery -provider openai -model gpt-4o-mini
```

## Output

```
attempt 1/5: running
  → grep: pattern not found (failure 1/4 for signature)
attempt 2/5: running
  → grep: pattern not found (failure 2/4 for signature)
attempt 3/5: running
  → grep: pattern not found
  ⚠ advisory: DecomposeGuardrail suggests: spawn_agent with researcher
attempt 4/5: running
  → grep: BLOCKED by guardrail (failure 4/4)
attempt 5/5: running with feedback
  → spawn_agent (researcher, explore)
  ← Child completed: Function NonExistentHandler not found in codebase
status=succeeded attempts=5 decompose_interventions=2
```

## Key Design Points

- **Signature canonicalization**: Same tool + same error kind = same signature
- **Tool-wide counter**: Catches "varies args every time" evasion
- **Advisory at 3rd**: Model gets one more chance with explicit guidance
- **Fatal at 4th**: Hard stop to prevent infinite loops
- **VerdictJudge**: Optional LLM that shapes the advisory message

## Testing

```sh
go test ./examples/stuck_recovery/
```

Tests verify:
- The verdict judge recommends spawning a researcher for grep failures
- Failure counting by signature, and that grep fails on a missing pattern
- Graduated escalation asserted directly against the guarded source:
  failures 1-2 pass through, 3 carries the judge's advisory, 4 is fatal
  (`TestDecomposeEscalation_PassAdviseFatal`)
