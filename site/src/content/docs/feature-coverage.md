---
title: Feature coverage
description: Map each major zkit and zarlcode capability to runnable code and deeper documentation.
---

Every advertised major capability should have a concrete repository example. The table
below is the coverage index; examples marked deterministic run without an LLM or network.

| Capability | Runnable example | Deeper guide | Runtime needs |
|---|---|---|---|
| Runner loop, goals, and verification | [`healthcheck`](https://github.com/zarldev/zarlmono/tree/main/examples/healthcheck) | [Runner](/zarlmono/runner/) | deterministic with `-scripted`; provider otherwise |
| Providers and model selection | [`healthcheck`](https://github.com/zarldev/zarlmono/tree/main/examples/healthcheck) | [Providers](/zarlmono/providers/) | provider for live mode |
| Schema, pre-call, and post-call guardrails | [`releasegate`](https://github.com/zarldev/zarlmono/tree/main/examples/releasegate) | [Guardrails](/zarlmono/guardrails/) | deterministic with `-scripted` |
| Human-in-the-loop approve / deny / edit | [`hitl_resume`](https://github.com/zarldev/zarlmono/tree/main/examples/hitl_resume) | [Shared infrastructure](/zarlmono/shared-infra/) | deterministic |
| Workflow graph execution and explicit resume | [`hitl_resume`](https://github.com/zarldev/zarlmono/tree/main/examples/hitl_resume) | [Shared infrastructure](/zarlmono/shared-infra/) | deterministic |
| Pursue, world-state oracle, and re-drive | [`hnupvote`](https://github.com/zarldev/zarlmono/tree/main/examples/hnupvote) | [Pursue](/zarlmono/pursue/) | live browser/provider |
| Spawned sub-agents and work modes | [`spawn_worker`](https://github.com/zarldev/zarlmono/tree/main/examples/spawn_worker) | [Spawn](/zarlmono/spawn/) | deterministic with `-scripted` |
| Stuck-agent decomposition recovery | [`stuck_recovery`](https://github.com/zarldev/zarlmono/tree/main/examples/stuck_recovery) | [Guardrails](/zarlmono/guardrails/) | deterministic with `-scripted` |
| Context compaction | [`long_conversation`](https://github.com/zarldev/zarlmono/tree/main/examples/long_conversation) | [Compaction](/zarlmono/compaction/) | deterministic with `-scripted` |
| Retrieval and code understanding | [`shared_infra`](https://github.com/zarldev/zarlmono/tree/main/examples/shared_infra) | [Shared infrastructure](/zarlmono/shared-infra/) | deterministic |
| MCP initialization, discovery, calls, disconnect | [`local_mcp`](https://github.com/zarldev/zarlmono/tree/main/examples/local_mcp) | [Tool ecosystem](/zarlmono/tool-ecosystem/) | deterministic |
| Dynamic tool lifecycle | [`dynamic_tools`](https://github.com/zarldev/zarlmono/tree/main/examples/dynamic_tools) | [Code tools](/zarlmono/code-tools/) | local shell |
| Skills discovery and selection | [`skill_catalog`](https://github.com/zarldev/zarlmono/tree/main/examples/skill_catalog) | [Tool ecosystem](/zarlmono/tool-ecosystem/) | deterministic |
| Notification subscription and offline drain | [`notify_drain`](https://github.com/zarldev/zarlmono/tree/main/examples/notify_drain) | [Shared infrastructure](/zarlmono/shared-infra/) | deterministic |
| Kernel-enforced shell sandbox | [`sandboxed_shell`](https://github.com/zarldev/zarlmono/tree/main/examples/sandboxed_shell) | [Sandboxing](/zarlmono/sandboxing/) | Linux Landlock |
| Runner lifecycle JSONL tracing | [`deterministic_trace`](https://github.com/zarldev/zarlmono/tree/main/examples/deterministic_trace) | [Shared infrastructure](/zarlmono/shared-infra/) | deterministic |
| Computer observe/act loop | [`computer_use`](https://github.com/zarldev/zarlmono/tree/main/examples/computer_use) | [Tool ecosystem](/zarlmono/tool-ecosystem/) | Chrome, network, provider |
| Durable sessions, resume, and export | [`zarlcode`](https://github.com/zarldev/zarlmono/tree/main/zarlcode) | [Sessions and transcripts](/zarlmono/sessions-transcripts/) | local SQLite |

## Start with deterministic examples

```bash
go run -C examples ./hitl_resume
go run -C examples ./local_mcp
go run -C examples ./dynamic_tools
go run -C examples ./skill_catalog
go run -C examples ./notify_drain
go run -C examples ./deterministic_trace
go run -C examples ./shared_infra
```

Then run the complete example test suite:

```bash
go test -C examples -count=1 ./...
```

The sandbox example is also deterministic, but its enforcement mechanism requires a
Linux kernel with Landlock:

```bash
go run -C examples ./sandboxed_shell
```
