---
name: go-zkit
description: Current zarlmono zkit package discovery and usage reference for both reusable library development and applications consuming github.com/zarldev/zarlmono/zkit APIs.
---

# ZarlMono Zkit Usage

[go-style](../go-style/SKILL.md) remains authoritative for architecture and ownership. This reference covers discovery and use of the current `github.com/zarldev/zarlmono/zkit` module.

## Inspect before use

Never infer a package API from memory. Inspect:

1. the consuming module's `go.mod` and root `go.work`;
2. the selected zkit version or local workspace replacement;
3. owning nested instructions, package documentation, and exported constructors;
4. tests demonstrating ownership, errors, and shutdown;
5. the package dependency direction.

The local workspace joins tooling, `examples`, `swebench-eval`, `zarlcode`, and `zkit`. Use module-specific checks; root `./...` does not cross module boundaries. Import only packages present in the selected module version.

## Current package namespace

Shared packages use:

```text
github.com/zarldev/zarlmono/zkit/<package>
```

Current discovery targets include:

```text
agent       ai          cache       db          docstore
filesystem  mcp         messagebus  oauth       options
prefs       skills      sourcecode  tui         vault
vectorstore zapp        zenv        zexec       zhttp
zlog        znotify     zrpc        zsync
```

Some targets contain subpackages rather than an importable root. In particular, inspect actual paths such as `agent/runner`, `ai/llm`, and `vectorstore/qdrant`; do not turn this directory inventory into unverified imports.

## Building zkit versus consuming it

Apply [go-style](../go-style/SKILL.md#north-star) for the distinction between reusable library capabilities and application domain operations, and its interface/construction rules for acyclic dependencies and options. Do not reinterpret those policies as a ban on existing generic zkit APIs.

Inspect each constructor/opener's actual ownership contract: not every shared object has `Close` or identical borrowing/transfer semantics. Package-specific anchors below capture the differences; they are not a universal lifecycle shape.

## Current API anchors

These are source-checked discovery anchors, not a substitute for reinspection when code changes.

| Need | Actual API and contract to inspect |
|---|---|
| Functional options | `options.Option[T]` is `func(*T)`. Use it when options are useful, not as mandatory ceremony; existing APIs target both concrete types and private config. |
| Synchronized map | `zsync.Map[K, V]` has a usable zero value; `NewMap[K, V]()` returns a pointer. `Get(key) (V, error)` uses `zsync.ErrNotFound`; `LoadOrStore(key, value) (V, bool)` is also supported. Never copy after use. Entry locking and shallow snapshots do not isolate nested mutable values or make multi-call transitions atomic. |
| Cache | `cache.NewMemoryCache[K, V]()` returns `*MemoryCache[K, V]`. `Get(ctx, key)` and `Set(ctx, key, value)` are context-aware; absence is `cache.ErrNotFound`. `Reader`, `Writer`, `ReadWriter`, and `Cache` are existing library capabilities. The memory cache retains values without deep cloning; choose alias ownership deliberately. |
| SQLite state | See [go-data](../go-data/SKILL.md#current-zarlmono-sqlite-persistence) for `zkit/db` ownership and typed APIs, and its SQLC reference for generation. |
| Document storage | See [go-data](../go-data/SKILL.md#mongodb-and-document-stores) for concrete stores, clone contracts, and borrowed Mongo collections. |
| HTTP | `zhttp.NewClient(opts ...options.Option[Client]) *Client`; `WithTimeout` sets the per-request timeout. Inspect retry, replay, and method policy before use. `NewServer(addr, handler, opts...)` returns `*http.Server`, so standard server shutdown still applies. |
| Application lifecycle | `zapp.New[T](program Program[T], opts ...options.Option[App[T]]) *App[T]`; `Run(ctx) int` runs and attempts cleanup. `AddCloser` and `AddContextCloser` register named resources and can report closed state or duplicate names. `Close(ctx)` follows reverse registration order and joins errors. Register dependents after dependencies or explicitly coordinate shutdown; this helper does not infer a dependency graph. An expired cleanup context prevents later closer calls, so do not assume unconditional cleanup after timeout. |
| Trusted endpoint construction | `vectorstore/qdrant.ParseEndpoint(raw) (Endpoint, error)` establishes a semantic endpoint; `NewClient(endpoint) *Client` assembles it. `NewClientWithZHTTP(endpoint, h)` accepts a prebuilt HTTP client. See [construction reference](../go-style/references/construction.md). |

Other focused discovery targets: `filesystem` for file policy, `messagebus` for messaging, `zlog` for structured logging, `zenv` for environment input, `zexec` for process execution, and `vault` for secrets. Inspect exact APIs, backend semantics, and ownership before selecting one; do not infer a universal constructor/options/close shape.

## Repository skill discovery

`zarlcode/catalog` loads standard `<name>/SKILL.md` files with nonempty YAML `name` and `description`. Its workspace skill directory is `<workspace>/.zarlcode/skills`, loaded after source-family and user skill directories so exact workspace entries override matching names. No extra configuration is needed for this set when zarlmono is the selected workspace.

A session rooted at `/home/bruno/src` still selects that workspace's catalogue; it does not recursively discover this repository's skill directory. Use zarlmono as the workspace to select these local overrides. Migration does not alter user/global or source-family skills.

## Review checklist

- Does the consuming module select the package being imported?
- Was the current exported API and owning guidance inspected?
- Is the standard library sufficient?
- Is this a reusable library capability or an application domain operation?
- Are aliasing, resource ownership, and dependency-based cleanup explicit?
- Do errors and cancellation map into the consumer's documented contract?
- Is the dependency direction consistent with the current module?
