# spawn_worker

A worked example of **hierarchical agent decomposition** using `agent_spawn`. The parent coordinates a complex refactor by delegating to specialized child agents, each running in a different work mode with appropriate tool gating.

## The Scenario

**Task**: "Refactor the authentication system to use JWT tokens"

The parent agent doesn't do the work itself. Instead, it:
1. Spawns a **researcher** (explore mode) to understand the current auth implementation
2. Spawns a **reviewer** (verify mode) to validate the refactor plan
3. Spawns a **coder** (implement mode) to implement the changes
4. Verifies the outcome by checking the filesystem

## What It Demonstrates

| Concept | File | What to Look For |
|---------|------|------------------|
| Named agents | `workers.go` | `AgentResolver` maps names to runners with different prompts/models |
| Mode enforcement | `harness.go` | `WithModeToolPolicy` gates tools by `spec.Mutates` |
| Parallel spawn | `harness.go` | Parent emits multiple `agent_spawn` calls in one turn |
| Depth limiting | `harness.go` | `WithMaxDepth(1)` prevents children from spawning grandchildren |
| Child result aggregation | `tools.go` | Parent receives structured summaries from each child |
| World verification | `harness.go` | Oracle checks filesystem state, not model claims |

## Architecture

```
Parent Runner (coordinator)
  └── agent_spawn tool
        ├── researcher runner (explore mode) → read-only
        ├── reviewer runner (verify mode) → read + test
        └── coder runner (implement mode) → full surface
              └── Actually edits files
```

## Running

```sh
# Deterministic scripted mode (no LLM)
go run ./examples/spawn_worker -scripted

# Real provider (OpenAI; OPENAI_MODEL defaults to gpt-4o-mini)
OPENAI_API_KEY=sk-... go run ./examples/spawn_worker
```

## Output

```
→ agent_spawn (researcher, explore)
  ← Child completed: Found auth in auth.go, session.go (2 files, 47 lines)
→ agent_spawn (reviewer, verify)
  ← Child completed: Plan approved: extract JWT logic to jwt.go, update middleware
→ agent_spawn (coder, implement)
  ← Child completed: Created jwt.go (89 lines), modified auth.go (3 changes)
✓ All children completed, files verified on disk
status=succeeded attempts=1 children=3 files_modified=2 files_created=1
```

## Key Design Points

- **Asynchronous launch**: `agent_spawn` returns a receipt immediately; the parent may continue independent work
- **Explicit collection**: `agent_status` polls without waiting and `agent_await` joins for the final summary
- **Children cannot spawn children**: Depth cap at 1 keeps the tree flat
- **Mode is enforced**: Explore child literally cannot write files (tool gate)
- **Workspace access is coordinated**: read-only children may overlap; implement work is exclusive
- **Results are structured**: Each terminal task retains summary, iterations, and reason
- **Parent aggregates**: Coordinator checks filesystem to verify outcomes

## Testing

```sh
go test ./examples/spawn_worker/
```

Tests verify:
- Mode policy: explore/verify block mutating tools, implement allows them
- Worker registries expose only the tools each mode permits
- The agent resolver maps known worker names to runners (unknown → nil)
- Full workflow (`TestSpawnWorker_Integration`): the parent spawns a coder
  worker that writes the files on the shared filesystem, then the parent
  completes and the oracle confirms the refactor on disk

The depth limit (`WithMaxDepth(1)`) and parallel/multi-worker coordination
are wired in `harness.go` but exercised only by the demo `main.go`, not
asserted by a test.
