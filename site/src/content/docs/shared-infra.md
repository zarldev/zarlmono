---
title: Shared agent infrastructure
description: Retrieval, workflows, checkpointing, HITL, and tracing packages for building agent systems beyond the core loop.
---

The runner is intentionally small: it loops, streams, dispatches tools,
and emits events. The next layer of shared infrastructure lives beside
it, not inside it. These packages are opt-in building blocks for agents
that need RAG, deterministic workflows, resumable state, human review,
or traces.

## Package map

| Package | What it provides |
|---|---|
| `zkit/ai/retrieval` | Product-neutral RAG primitives: `Document`, `Chunker`, `Embedder`, `VectorStore`, `Retriever`, `Reranker`, an indexing `Pipeline`, and an in-memory cosine vector store for tests/local corpora. |
| `zkit/agent/retrieval` | Agent adapters for retrieval: render retrieved docs as prompt context, expose a retriever as a `PromptSource`, or expose it as a `retrieve_context` tool. |
| `zkit/agent/workflow` | Typed graph/workflow composition: nodes, static edges, conditional routes, compiled `Runnable`, execution state, event sink, and workflow-as-tool adapter. |
| `zkit/agent/checkpoint` | Transport-neutral run snapshots and a concurrent in-memory `Store`. Durable filesystem/SQLite stores can implement the same interface later. |
| `zkit/agent/hitl` | Human-in-the-loop request/review model: risk levels, decisions, reviewer patches/comments, and policy hooks like `RequireHuman` and `ApproveLowRisk`. |
| `zkit/agent/trace` | Normalized trace events, exporters, JSONL output, and adapters from runner/workflow event sinks. |

The split matters: `zkit/ai/retrieval` has no dependency on the agent
runtime, while `zkit/agent/retrieval` is where retrieved documents
become prompts or tools.

## Retrieval and indexing

`zkit/ai/retrieval` defines the reusable substrate. A typical local
pipeline chunks documents, embeds each chunk, and stores the vectors:

```go
store := retrieval.NewMemoryVectorStore()
pipe := retrieval.Pipeline{
    Chunker:  retrieval.TextChunker{Size: 1_000, Overlap: 100},
    Embedder: embedder, // your OpenAI/Ollama/local implementation
    Store:    store,
}

err := pipe.Index(ctx, []retrieval.Document{{
    ID:   "readme",
    Text: body,
    Metadata: retrieval.Metadata{"source": "README.md"},
}})
```

Query-time retrieval composes an `Embedder` and `VectorStore`:

```go
r := retrieval.VectorRetriever{
    Embedder: embedder,
    Store:    store,
    Limit:    5,
}

docs, err := r.Retrieve(ctx, "how does checkpointing work?")
```

Use typed metadata filters when callers need a constrained subset:

```go
docs, err := r.Retrieve(ctx, "deployment rollback",
	retrieval.WithFilter(retrieval.Filter{
		Must: []retrieval.Condition{
			retrieval.Eq("source", "README.md"),
		},
	}),
)
```

The in-memory store uses cosine similarity. It is useful for examples,
tests, and small local corpora; production backends should implement
`VectorStore` against Qdrant, pgvector, SQLite, or a hosted service.

## Retrieval in an agent

Agent-facing retrieval stays separate from the runner. Use
`agent/retrieval.PromptSource` when retrieved context should be part of
the system prompt:

```go
source := agentretrieval.PromptSource{
    Retriever: r,
    Format: agentretrieval.FormatOptions{
        Title:      "Relevant project context",
        MaxDocs:    5,
        MaxRunes:   2_000,
        ShowScores: true,
    },
}

runner.New(client, runner.WithPrompt(source))
```

Or expose retrieval as a normal tool so the model decides when to ask
for context:

```go
tool := agentretrieval.Tool{Retriever: r}
reg.Register(tool)
```

## Workflows

`zkit/agent/workflow` is for deterministic orchestration around agents:
validate input, retrieve context, call a model, post-process output, or
branch based on state. It does not replace the runner; it composes with
it.

```go
g := workflow.NewGraph()

workflow.AddNode(g, "validate", workflow.NodeFunc[Input, Input](validate))
workflow.AddNode(g, "answer", workflow.NodeFunc[Input, Output](answer))

g.AddEdge(workflow.Start, "validate")
g.AddEdge("validate", "answer")
g.AddEdge("answer", workflow.End)

run, _ := g.Compile()
out, state, err := run.InvokeState(ctx, input)
```

Conditional routes can loop or branch, and compiled workflows can be
wrapped as tools for an agent. `Runnable.Sink` emits workflow lifecycle
and per-node events for UIs and trace exporters.

## Checkpoints and HITL

Checkpointing is deliberately just the shared contract:

```go
store := checkpoint.NewMemoryStore()
err := store.Save(ctx, checkpoint.Checkpoint{
    ID:    "approval-1",
    RunID: "run-42",
    Step:  "before-edit",
    State: map[string]any{"file": "main.go"},
})
```

`agent/hitl` models the review plane without choosing a transport:

```go
req := hitl.Request{
    ID:           "review-1",
    RunID:        "run-42",
    CheckpointID: "approval-1",
    Action:       "edit_file",
    Summary:      "Rewrite the config loader",
    Risk:         hitl.RiskMedium,
}

review, decided, err := hitl.ApproveLowRisk{}.Review(ctx, req)
if !decided {
    // send req to a TUI, web UI, notification queue, etc.
}
```

zarlcode and zarlai can route these requests through their own UI/API
surfaces while sharing the same request, decision, and checkpoint types.

## Tracing

`agent/trace` normalizes runner and workflow events into one stream.
Start with JSONL when debugging locally:

```go
exporter := trace.NewJSONLExporter(os.Stdout)
sink := &trace.Sink{Exporter: exporter}

r := runner.New(client,
    runner.WithTools(tools),
    runner.WithSink(sink),
)
```

Workflow events use the sibling adapter:

```go
run.Sink = &trace.WorkflowSink{Exporter: exporter}
```

Exporters are intentionally tiny. OpenTelemetry, Langfuse, or an
application-specific timeline can implement the same `Exporter`
interface without changing runner or workflow code.
