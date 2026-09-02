# Local MCP over stdio

A deterministic Model Context Protocol example with no network, credentials, or
LLM. The executable runs in two roles: the parent is a `zkit/mcp` client and a
child process is a tiny local MCP server speaking newline-delimited JSON-RPC over
stdin/stdout.

The client demonstrates the complete local lifecycle:

1. launch and initialize the stdio server with `mcp.NewStdioClientContext`;
2. discover its `echo` tool;
3. call the tool and read its text result;
4. close the client, which closes stdin and waits for the child server to exit.

The server rejects discovery or calls before initialization. Its scanner loop is
owned by the child process and ends on stdin EOF, while the client's `Close`
provides the shutdown and wait path.

## Run

```sh
go run -C examples ./local_mcp
```

Expected output:

```text
connected: initialization complete
discovered: echo
result: echo: hello, MCP
disconnected: server stopped
```

## Test

The external black-box test builds and runs the executable, checks the complete
output, and uses a deadline to catch cleanup failures.

```sh
go test -C examples ./local_mcp
```
