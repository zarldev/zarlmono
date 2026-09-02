# Examples

Worked examples for building systems on top of the zarlcode runner, tool, guardrail, pursue, retrieval, workflow, checkpoint, HITL, and trace packages.

| Example | What it shows | External dependencies |
|---|---|---|
| [`hnupvote`](hnupvote/) | Real browser automation harness with auth guardrail, world-state oracle, and re-drive against Hacker News. | Chrome, HN account, LLM backend. |
| [`releasegate`](releasegate/) | Release-publishing harness with a real LLM by default, JSON schema validation, pre-call guardrail, post-call guardrail, and scripted deterministic test mode. | LLM backend for default run; none with `-scripted`. |
| [`healthcheck`](healthcheck/) | Infrastructure monitoring with SchemaGuardrail + FanoutGuardrail, transient-failure auto-retry, and all-healthy oracle. | LLM backend for default run; none with `-scripted`. |
| [`spawn_worker`](spawn_worker/) | Hierarchical agent decomposition with named workers, mode enforcement (explore/verify/implement), and capability-based tool gating. | LLM backend for default run; none with `-scripted`. |
| [`stuck_recovery`](stuck_recovery/) | DecomposeGuardrail graduated response (pass → advise → fatal) with VerdictJudge for stuck-agent recovery. | LLM backend for default run; none with `-scripted`. |
| [`long_conversation`](long_conversation/) | Compactor integration with structural trimming — the agent researches a large codebase and the runner automatically compacts stale context. | LLM backend for default run; none with `-scripted`. |
| [`shared_infra`](shared_infra/) | No-LLM tour of deterministic code understanding (`file_map` AST outline + `retrieve_code` lexical retrieval via go/parser), retrieval indexing/search, agent context formatting, workflow graph execution, checkpoint/HITL review, and JSONL tracing. | None. |
| [`computer_use`](computer_use/) | LLM-generated Wikipedia quiz solved through the universal computer-use loop: `computer_observe` reads the UI, the LLM chooses an answer, and `computer_act` clicks it. | Chrome, Wikipedia network access, LLM backend. |
| [`hitl_resume`](hitl_resume/) | Application-owned workflow checkpoint, HITL approve/deny/edit review, reload, and explicit continuation. | None. |
| [`local_mcp`](local_mcp/) | Local stdio MCP subprocess: initialize, discover tools, call one, and disconnect cleanly. | None. |
| [`dynamic_tools`](dynamic_tools/) | Filesystem-backed dynamic tool registration, invocation, reload, unregister, and collision rejection. | Local shell only. |
| [`skill_catalog`](skill_catalog/) | Discover SKILL.md files, load them into the versioned skill store, and select applicable guidance. | None. |
| [`notify_drain`](notify_drain/) | Live notification subscriptions, unsubscribe, and deterministic offline drain semantics. | None. |
| [`sandboxed_shell`](sandboxed_shell/) | Kernel-enforced workspace confinement: allowed in-workspace write and denied outside write. | Linux Landlock. |
| [`deterministic_trace`](deterministic_trace/) | Runner callback events written to JSONL, read back, and counted deterministically. | None. |

See [`patterns.md`](patterns.md) for the patterns these examples demonstrate and how to apply them in your own systems.

Run all example tests:

```sh
go test -C examples ./...
```
