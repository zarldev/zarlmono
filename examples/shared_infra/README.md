# shared_infra

A deterministic, no-LLM example for the shared zkit agent infrastructure added
around the runner:

- `zkit/ai/retrieval`: chunk/embed/index/search documents with an in-memory vector store.
- `zkit/agent/retrieval`: format retrieved documents for an agent prompt.
- `zkit/agent/workflow`: compose deterministic steps into a graph.
- `zkit/agent/checkpoint`: snapshot workflow state before a risky action.
- `zkit/agent/hitl`: defer medium-risk actions to a human reviewer.
- `zkit/agent/trace`: export workflow events as JSONL.

Run it:

```sh
go run ./shared_infra
```

Run the test:

```sh
go test ./shared_infra
```
