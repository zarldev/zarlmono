# MCP protocol and transport package

`mcp` provides JSON-RPC 2.0 client/server primitives for the Model Context Protocol. It owns HTTP and stdio transports, negotiation, tool discovery and invocation, typed tool-result content, resource reads, and server-pushed notifications. Agent-facing connection registration and remote-tool wrapping live in `zkit/ai/tools/dynamic` and `zkit/ai/tools`.

## Supported protocol revisions

Negotiating clients prefer revision `2026-07-28` through `server/discover`. On HTTP, that revision uses request `_meta` identity/capability metadata mirrored by `MCP-Protocol-Version`, `Mcp-Method`, and, for named calls, `Mcp-Name` headers. A `tools/call` also mirrors primitive arguments annotated with `x-mcp-header` into `MCP-Param-*` headers after loading the discovered tool schemas. HTTP notification subscriptions use a `subscriptions/listen` POST whose response is an SSE stream.

If discovery reports method-not-found, the client uses `initialize` requesting `2025-11-25`, sends `notifications/initialized`, and tracks an HTTP session returned by the server. Stdio negotiation also accepts an initialized server selecting `2024-11-05`. The package server advertises `2026-07-28` and `2025-11-25` from discovery and selects `2025-11-25` from `initialize`.

These revisions describe the wire paths implemented here, not support for every optional MCP service. The high-level client exposes:

- `Discover` for `tools/list`;
- `Call` for `tools/call`;
- `ReadResource` for `resources/read`;
- `Subscribe` and `SubscribeAny` for server-pushed notifications.

The package server dispatches discovery, initialization, `tools/list`, and `tools/call`, and publishes notifications. It does not provide prompt handlers, resource catalog handlers, sampling, roots, elicitation, or a general optional-service framework.

## Transports and lifecycle

HTTP calls use POST and accept either JSON or SSE responses. Registering the first notification handler lazily starts one reconnecting SSE listener; `Client.Close` cancels it, waits for active calls and the listener, attempts HTTP session deletion, and closes idle connections. Redirects are rejected. Use `NewClientWithDialPolicy` when resolved addresses require filtering, or `NewLoopbackClient` for an operator-approved endpoint that must remain loopback-only and ignore proxy configuration. `NewClient` applies no per-dial address filter and is intended only where another trusted boundary supplies equivalent policy.

`NewStdioClientContext` launches a subprocess with the explicitly supplied environment layered over `zexec.MinimalEnv`, performs a bounded initialization handshake, and tears the process down if initialization fails or is canceled. A successful client owns the subprocess; callers must call `Close`, which terminates it and waits for the reader and stderr-drain workers. Stdio stderr is length-capped and credential-shaped values are redacted before debug logging.

Notification callbacks run on transport-owned reader/listener work and must not block. Dispatch expensive work to caller-owned, bounded work and provide its cancellation and wait path.

## Tool and result boundaries

Discovered input schemas remain JSON-shaped so constraints survive into tool registries. Unknown result content types are skipped for forward compatibility; known text, image, audio, and resource items preserve wire order in `CallResult.Content`. `CallResult.IsError` is a tool-reported failure and is distinct from the Go error returned for transport or JSON-RPC failures.

Remote MCP descriptions, schemas, notifications, and results are untrusted data. The protocol package does not turn them into local authorization. The tools bridge conservatively marks remote tools mutating and preserves local guardrail, workspace, and approval policy.
