---
title: Tool ecosystem
description: Beyond the workspace code tools — the typed builder every tool is made with, runtime tool authoring, web fetch, and web search.
---

The [code tools](/zarlmono/code-tools/) are the workspace-scoped core,
but `zkit/ai/tools` is larger than that. Four sibling packages cover
the rest of what an agent reaches for: building tools (at author time
*and* at runtime), connecting to other tool servers, and reaching the
web.

## toolkit — the typed builder

Start here, because almost everything else is built on it.
`zkit/ai/tools/toolkit` turns a typed Go function into a `tools.Tool`,
so you never hand-write a JSON Schema:

```go
type Args struct {
	Path string `json:"path" doc:"file to read, workspace-relative"`
	Max  int    `json:"max,omitempty" doc:"max lines to return"`
}

tool := toolkit.Tool[Args, string]{
	Name:        "head",
	Description: "Read the first lines of a file.",
	Func:        func(ctx context.Context, a Args) (string, error) { /* … */ },
}
```

`SchemaFor[Args]` reflects the struct into the schema — `json` tags
name fields, `doc:` / `description:` annotate them, `enum:"a,b,c"`
constrains them, and pointer or `omitempty` fields become optional.
Decoding is repair-aware, tolerating the trailing commas and stray
newlines small models emit. When a schema needs `oneOf` / `$ref` and
reflection can't express it, implement the two-method `Handler`
interface by hand — same dispatch, full control.

## dynamic — tools the agent writes for itself

`zkit/ai/tools/dynamic` lets a running agent extend its own tool
surface. The headline tool is `new_tool`: the agent supplies a name, an
args schema, and a Go function body; the package renders a complete
`main.go` from a `text/template`, `go build`s it, and registers the
result. It's **not** a `plugin`-package trick — each dynamic tool is a
standalone binary that speaks a tiny protocol:

```
mytool --describe       → prints its tools.ToolSpec as JSON
mytool --call  (stdin)  → reads args JSON, writes {"data": …} or {"error": …}
```

`toolkit.Run` implements both sides of that contract, so a generated
tool is a handful of lines wrapped around the author's `Func`.
Execution is capped — a 60s timeout, 1 MB stdout, a minimal
environment, process-group kill on timeout — because a dynamic tool is
untrusted code the agent just wrote.

Persistence is a **`Catalog`** (SQLite in production, a JSON file under
test) bridged to the live registry by a **`Registrar`**. On startup
`Sync` rebuilds the registry from the catalog; built-in tools always
win a name collision, so a stale catalog entry can never shadow a real
tool. Everything dynamic registers under one provider tag, so a UI can
list or clear the whole set at once.

### MCP connections

The same package speaks [MCP](/zarlmono/foundation/#shared-infrastructure):
`mcp_connect` dials a server (stdio or HTTP), discovers its tools, and
registers them; `mcp_disconnect` and `mcp_list` manage the rest.
Connection is policy-gated before any process or socket opens — the
default policy rejects `localhost` and private-range HTTP targets,
refuses to send a bearer token over cleartext, requires an absolute
command path for stdio, and re-checks the resolved IP at dial time to
defeat DNS rebinding. Discovered tools are bounded (count, description
length, schema size) and can't shadow existing names.

## fetch — web_fetch

`zkit/ai/tools/fetch` provides `web_fetch`: an HTTP GET that returns
extracted page text, not raw HTML. It runs on
[`zhttp`](/zarlmono/foundation/#core) with a tight timeout and
**two-layer SSRF protection** — a pre-flight host/IP check *and* a
dial-time `Control` hook that re-validates the resolved address, so a
hostname that passes the first check but resolves to `127.0.0.1` (or
rebinds mid-request) still can't reach internal services.

When a plain GET comes back nearly empty (≤512 bytes of text — the
signature of a JavaScript app shell), or the caller asks for it
explicitly, fetch falls back to a headless **chromedp** browser:
resolve a Chrome binary, render in an ephemeral profile, settle ~1.5s
for hydration, and extract `innerText`. Output is capped (50k chars by
default) with sentence-boundary truncation.

## search — web_search

`zkit/ai/tools/search` provides `web_search` against a local SearXNG
instance (`/search?format=json`). Results default to a **labelled**
plaintext format — numbered title/URL/snippet triples — rather than
JSON, because that mirrors the search UIs models are trained on and
lets them refer to "result 2" without token-heavy array indexing; pass
`output=json` for structured results. Failures (no endpoint configured,
empty query, a 5xx from the backend) come back as typed
`tools.ToolResult`s, never a Go `error`, so a failed search is
something the model reasons about instead of something that kills the
loop.
