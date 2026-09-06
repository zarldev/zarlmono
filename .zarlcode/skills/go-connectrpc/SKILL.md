---
name: go-connectrpc
description: ConnectRPC, Protocol Buffer, and Buf reference for zarlmono contracts, generation, handlers, mapping, error codes, interceptors, streaming, and verification where that stack is used.
---

# ConnectRPC and Protobuf Reference

[go-style](../go-style/SKILL.md) governs domain boundaries, ownership, cancellation, logging, and lifecycle. This skill covers ConnectRPC-specific contracts and mechanics where the owning package uses that stack; it does not require every zkit transport to adopt it.

## Contract layout

Keep source protobuf files and Buf configuration separate from generated Go and client outputs. Never edit generated files. Illustrative layout:

```text
proto/
  buf.yaml
  buf.gen.yaml
  account/v1/account.proto
internal/transport/connect/
  gen/
  server.go
```

Derive `go_package` from the consuming module's actual path and follow existing repository layout. Do not copy a placeholder module path.

## Protobuf design

- Use versioned packages.
- Name request and response messages after RPC methods.
- Prefer domain actions over mechanical CRUD names when the domain has meaningful behavior.
- Reserve removed field numbers and names.
- Use protobuf well-known types where their semantics fit.
- Treat compatibility and breaking checks as part of the contract.

## Generation

Pin Buf and plugins through the repository's tool mechanism when using them. Use repository-local paths and deterministic output; inspect the owning configuration rather than assuming these example commands are configured everywhere.

Typical checks:

```bash
buf format -w
buf lint
buf generate
buf breaking --against '<configured baseline>'
```

Inspect generated diffs and run focused Go tests after generation.

## Handlers

Connect handlers are boundary adapters. They:

1. parse and validate request representation;
2. map to typed domain input;
3. call a narrow consumer capability;
4. map domain output to protobuf;
5. map semantic errors to Connect codes.

Runtime domain transitions still check current state and authorization at the owner; parsing a valid request does not guarantee that a transition can succeed.

Declare conformance to the generated handler interface beside the server type when imports remain acyclic; otherwise use an external integration test or composition boundary.

Keep mapping functions explicit. Do not expose generated messages in application domain services or persist them as domain models. Reusable transport packages may legitimately own generated protocol representations.

## Error mapping

Map domain identities with `errors.Is`/`errors.As`:

| Domain outcome | Connect code |
|---|---|
| invalid input | `connect.CodeInvalidArgument` |
| unauthenticated | `connect.CodeUnauthenticated` |
| forbidden | `connect.CodePermissionDenied` |
| not found | `connect.CodeNotFound` |
| conflict | `connect.CodeAlreadyExists` or `connect.CodeAborted`, according to semantics |
| unexpected internal failure | `connect.CodeInternal` |

Preserve useful causes internally without exposing sensitive implementation detail to clients. Follow [go-errors](../go-errors/SKILL.md) for the distinction between diagnostic causes and stable public identities.

## Interceptors and logging

Use interceptors for transport-wide authentication, request metadata, and consumed boundary logging. Do not duplicate logging in handlers and domain services. Expected client/domain outcomes generally map without error-level logs.

## Streaming

For each stream, define:

- producer and channel ownership;
- cancellation and client-disconnect behavior;
- send failure behavior;
- backpressure and buffering;
- who waits for producer shutdown;
- who closes any internal channels.

Observe the request context. A normal client disconnect may end the handler without an application error; domain producers still need an explicit stop and wait path. Never leave a producer goroutine running after the stream returns. Coordinate draining and cancellation so shutdown does not wait forever on a blocked sender, and close dependencies only after their users stop.

## Verification

- Format, lint, generate, and run configured breaking checks when contracts change.
- Compile generated interfaces and handlers.
- Test success, validation, each semantic error mapping, authentication, cancellation, and stream shutdown in external test packages as required here.
- Review generated output rather than editing it.

## Review checklist

- Does `go_package` match the actual module?
- Are application domain and generated representations separate?
- Are handler capabilities narrow and conformance acyclic?
- Are semantic errors mapped consistently?
- Is unexpected failure logged once at the transport boundary?
- Does every streaming producer stop and get waited?
- Were generation and breaking checks run with configured pinned tools?
