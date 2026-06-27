---
title: Foundation packages
description: The non-AI substrate — env, sync, HTTP, notifications, cache, storage, pub/sub, MCP — that the agent stack stands on.
---

The agent runtime is the loud part of zkit, but it stands on a layer
of small, boring, dependency-light packages. Boring is the feature:
each one does a single job, most fit in one file, and none of them
know the others exist. They're independently importable — if all you
want is the concurrent map, take the concurrent map.

## Core

| Package | What it is |
|---|---|
| `options` | The one true functional-options type: `Option[T] func(*T)`. Every constructor in the repo uses it. |
| `zenv` | Typed env access with defaults: `zenv.String`, `zenv.Int`, `zenv.Duration`, `zenv.Bool`, generic `zenv.Get`. Parse failure falls back to the default — deliberately forgiving, deliberately not a config system. |
| `zsync` | Generic concurrency primitives: `Map[K,V]`, `Queue[T]` (blocking pop with ctx), `Set[T]`. The mutex boilerplate you stop writing. |
| `zhttp` | HTTP client with a real retry policy — exponential backoff, jitter, `Retry-After` honoured — behind options. Used by the fetch tool and the Qdrant client. |
| `zlog` / `zrpc` / `zapp` / `zexec` / `processenv` | Logging setup, RPC helpers, app lifecycle, minimal-env exec, process environment. Plumbing with opinions. |

```go
port := zenv.Int("PORT", 8080)
queue := zsync.Queue[Job]{}        // Push / PopContext / Close
client := zhttp.NewClient(zhttp.WithRetryPolicy(zhttp.NoRetry()))
```

## Shared infrastructure

| Package | What it is |
|---|---|
| `mcp` | Model Context Protocol client *and* server, with transports. The agent's MCP tools sit on this, but it's usable standalone. |
| `znotify` | Session-keyed notification store: `Subscribe` / `Push` / `Broadcast` / `Drain`, with offline queueing for subscribers that come back. |
| `messagebus` | Typed pub/sub with in-memory and NATS implementations behind the same interface. |
| `cache` | Generic cache contract with memory, file, and Redis backends. |
| `docstore` | Typed document store — memory and MongoDB. |
| `filesystem` | Filesystem abstraction — memory, OS, SeaweedFS. The in-memory one is why so many tests need no disk. |
| `vectorstore/qdrant` | Qdrant client (built on `zhttp`). |

| `db` / `vault` / `prefs` / `oauth` | SQLite state, an encrypted key vault, settings, and OAuth login flows — the persistence layer the applications share. |
| `sandbox` | Kernel-enforced filesystem + network confinement via Landlock and user namespaces. Grants system read, workspace/tmp/cache write, denies everything else — including `~/.ssh` and `~/.aws`. Linux only. See [Sandboxing](/zarlmono/sandboxing/). |
| `shellpolicy` | Shell command vetting: parses bash into a platform-neutral IR and applies block rules (destructive patterns, output redirection, syntax errors). The policy layer in front of [sandboxing](/zarlmono/sandboxing/). |
| `skills` | Versioned, hot-reloadable store of markdown capability guides. The runner's prompt source consults it to inject skill descriptions when relevant. |
The pattern across all of these: a small interface defined where
it's consumed, an in-memory implementation that makes testing
trivial (fakes over mocks, always), and real backends behind the
same contract.

## Agent infrastructure

These packages sit between the foundation and the agent runtime —
they're used by the runner, guardrails, and tools, but don't depend
on the runner itself:

| Package | What it is |
|---|---|
| `sensor` | Periodic observers: one goroutine per sensor, returning `ErrNoChange` until an observation actually moves, so the runner reacts only to real deltas. Changed observations are injected into the steer queue as untrusted data. Used for ambient awareness (time-of-day, Home Assistant state, Spotify, MCP pushes). |
| `taskscope` | Task identity via context: `WithID(ctx, id)` plants a task ID; guardrails and compactors read it back to key per-task state. Also carries `WorkMode` (`explore` / `verify` / `implement`). |
| `diffrecorder` | Captures workspace diffs between tool calls. Reads `FileEffect` from tool results and builds per-turn and cumulative diffs for eval harnesses and audit trails. |
| `sourcechain` | Composes multiple `ToolSource` implementations with fallback semantics — primary source first, then fallbacks. Merges built-in, dynamic, and MCP tools into one surface. |
| `scheduler` | Cron-based task scheduling via `robfig/cron/v3`. Drives recurring background tasks with the same runner loop as interactive sessions. |
| `retrieval` | Agent-facing RAG glue: render retrieved docs as prompt context or expose a retriever as a tool. Core document/vector interfaces live in `ai/retrieval`. |
| `workflow` | Typed graph/workflow composition with conditional routing, events, and workflow-as-tool adapters. |
| `checkpoint` | Transport-neutral run snapshots and in-memory storage for resumable workflows. |
| `hitl` | Human-in-the-loop request/review/policy primitives. |
| `trace` | Normalized runner/workflow traces, exporters, and JSONL output. |

## The applications

zarlmono is a monorepo; zkit exists because three applications
demanded the same substrate and refused to let it drift:

- **[zarlcode](https://github.com/zarldev/zarlmono/tree/main/zarlcode)** —
  the terminal coding agent. The TUI over everything documented on
  this site: runner, guardrails, compaction, sandboxed shell,
  sub-agents, SQLite sessions.
- **[zarlai](https://github.com/zarldev/zarlmono/tree/main/zarlai)** —
  a local, multimodal assistant: speech in/out, vision, face
  recognition, home automation tools, autonomous background tasks —
  running the same runner loop against local inference.
- **[swebench-eval](https://github.com/zarldev/zarlmono/tree/main/swebench-eval)** —
  the SWE-bench evaluation driver. Builds its agent through the same
  shared assembly as the TUI, which is the point: the agent you eval
  is the agent you ship.

Two very different products and an eval harness pulling on the same
packages is the forcing function that keeps the interfaces small and
honest.
