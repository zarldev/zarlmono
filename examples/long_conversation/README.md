# long_conversation

A worked example of **compaction and context pressure** management. The agent researches a large codebase, accumulating tool results until the context window fills. The compactor triggers and preserves key findings while dropping stale details.

## The Scenario

**Task**: "Research the handlers.go file and document every public function"

The codebase has many files with verbose content. The agent:
1. Lists all files (1KB)
2. Reads docs.go - a large documentation file (4KB)
3. Reads utils.go - verbose helper functions (3KB)
4. Reads handlers.go - the target with many functions (5KB)
5. **Compaction triggers** - context pressure reduces verbatim history
6. Agent continues with compacted context and produces the documentation

## What It Demonstrates

| Concept | File | What to Look For |
|---------|------|------------------|
| Context pressure detection | `harness.go` | `shouldRunCompact` and token estimation |
| Proactive compaction | `harness.go` | `PressureGated` compactor with prober |
| Forced compaction | `harness.go` | Hard compaction when soft limit exceeded |
| Compacted iteration events | `harness.go` | `IterationCompleted` with `Compacted` flag |
| Summary preservation | `harness.go` | Keep-recent + summary strategies |
| Progress tracking | `harness.go` | EventSink captures compaction events |

## Architecture

```
Runner with PressureGated compactor
  └── Iteration 1: list files → 1KB in context
  └── Iteration 2: read docs.go → 5KB in context
  └── Iteration 3: read utils.go → 8KB in context
  └── Iteration 4: read handlers.go → 13KB in context
  └── [Probe: context pressure detected]
  └── Iteration 5: COMPACTION → 6KB of key findings kept
  └── Iteration 6: compile documentation → done
```

## Running

```sh
# Scripted mode (deterministic)
go run ./examples/long_conversation -scripted

# Real provider
go run ./examples/long_conversation -provider openai -model gpt-4o-mini
```

## Output

```
iteration 1: list_files → 1 file found
iteration 2: read docs.go → read 51 lines
iteration 3: read utils.go → read 34 lines
iteration 4: read handlers.go → read 73 lines
⚠ compaction triggered: kept 35 tokens, dropped 4231 tokens
iteration 4: compaction_applied true
iteration 5: publish results → done
status=succeeded documents_produced=4

Compaction summary:
  - 1 compaction event triggered
  - Final context: 3200 tokens (below 4096 limit)
```

## Key Design Points

- **Probe before compact**: The prober estimates token count and skips compaction if unnecessary
- **No-op latch**: Forced compactions that freed nothing are suppressed on re-entry
- **Keep-recent strategy**: Most recent N messages stay verbatim; older ones are summarized
- **Event audit**: Compaction events are visible in `IterationCompleted` payloads

## Testing

```sh
go test ./examples/long_conversation/
```

Tests verify:
- The read/list/push_docs tools track research progress correctly
- Driving the scripted multi-file research through the real runner +
  structural compactor emits a `CompactionApplied` event and resolves the
  goal (`TestRunLongConversation_FiresCompactionEvent`)
