# Dynamic tools lifecycle

This deterministic example exercises the current `zkit/ai/tools/dynamic` API without an LLM or network service. It uses a temporary workspace and JSON catalog to model an agent that:

1. calls `new_tool` to author, compile, and register a typed Go tool;
2. invokes the registered binary through `tools.Registry`;
3. creates fresh catalog/registry instances, then uses `LoadContext` and `Registrar.Sync` to model restart/reload;
4. verifies that a dynamic tool cannot collide with a non-dynamic registration;
5. calls `unregister_tool` and confirms the persistent catalog is empty.

The generated tool subprocesses are synchronously awaited by the dynamic-tool API, bounded by context timeouts, and the temporary workspace is removed on exit.

Run from the repository root:

```bash
go run -C examples ./dynamic_tools
go test -C examples ./dynamic_tools
```

Expected output:

```text
authored_and_registered=echo_upper
invoked=FIRST CALL
reloaded_and_invoked=AFTER RELOAD
collision=rejected
unregistered=echo_upper
catalog_entries=0
```
