# deterministic_trace

A deterministic, no-LLM/no-network tracing example. It connects one sink to
zkit's supported observability boundaries:

- `runner.EventSink` for conversation, content, iteration, and tool lifecycle
  events.
- `workflow.EventSink` for workflow and node lifecycle events.

The sink normalizes representative events into one JSONL artifact. It uses a
monotonic sequence number rather than wall-clock timestamps, so repeated runs
have stable ordering and content. The program closes the artifact, reads every
JSONL row back, and prints a summary.

Run it from the repository root:

```sh
go run -C examples ./deterministic_trace -out /tmp/zarl-trace.jsonl
```

Inspect the artifact directly:

```sh
cat /tmp/zarl-trace.jsonl
```

Run its external black-box tests:

```sh
go test -C examples ./deterministic_trace
```

The runner uses `runnertest.Client` and a canned tool, while the workflow is a
pure integer transformation. Neither path reads credentials or performs
network I/O.
