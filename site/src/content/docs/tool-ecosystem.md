---
title: Tool ecosystem
description: Typed in-process tools, standalone tool binaries, runtime tool authoring, web fetch, and web search.
---

The [code tools](/zarlmono/code-tools/) are the workspace-scoped core,
but `zkit/ai/tools` is larger than that. Four sibling packages cover
the rest of what an agent reaches for: building tools (at author time
*and* at runtime), connecting to other tool servers, and reaching the
web.

## New — in-process tools

Use `tools.New` for tools registered directly with a runner. Define argument
and result types, and generate the input schema with `tools.SchemaFor`:

```go
type UpperArgs struct {
	Text string `json:"text" doc:"Text to convert to uppercase"`
}

type UpperResult struct {
	Text string `json:"text"`
}

tool := tools.New(tools.ToolSpec{
	Name:        "upper",
	Description: "Convert text to uppercase.",
	Parameters:  tools.SchemaFor[UpperArgs](),
}, func(_ context.Context, args UpperArgs) (UpperResult, error) {
	return UpperResult{Text: strings.ToUpper(args.Text)}, nil
})
reg := tools.NewRegistry(tool)
```

`SchemaFor[Args]` reflects the struct into a schema: `json` tags name fields,
`doc:` / `description:` annotate them, `enum:"a,b,c"` constrains them, and
pointer or `omitempty` fields become optional. `New` decodes arguments
through the JSON repairer and packages the handler's typed result or error.
Validate domain rules in the handler; use a schema guardrail to enforce the
schema before dispatch.

## toolkit — the typed builder

`zkit/ai/tools/toolkit` is for **standalone tool binaries**, not in-process
`tools.Tool` values. Its `Tool[Args, Result]` implements the binary handler
contract, and `toolkit.Run` handles `--describe` and `--call`:

```go
func main() {
	toolkit.Run(toolkit.Tool[UpperArgs, UpperResult]{
		Name:        "upper",
		Description: "Convert text to uppercase.",
		Func: func(_ context.Context, args UpperArgs) (UpperResult, error) {
			return UpperResult{Text: strings.ToUpper(args.Text)}, nil
		},
	})
}
```

This binary uses the same argument and result structs as above. Unlike
`New`, its call decoder uses strict JSON decoding. For a custom binary
schema, implement toolkit's `Handler` (`Describe` and `Call`) directly and
pass it to `toolkit.RunHandler(handler)` in `main`. `toolkit.Run` only accepts
a typed `toolkit.Tool[Args, Result]`.

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

`zkit/ai/tools/search` provides one stable `web_search` contract with
swappable backends. `NewSearxng` queries a local SearXNG instance
(`/search?format=json`); `NewBrave` queries the Brave Search API, whose
independent licensed index avoids the upstream scraper throttling a
self-hosted metasearch instance can encounter. The application composition
root selects one backend, but both normalize transport-specific responses to
the same `Args`, `Hit`, and `Result` types, so changing providers does not
change the model-facing schema or result format.

Results default to **labelled** plaintext — numbered title/URL/snippet triples
— because that mirrors the search UIs models are trained on and lets them
refer to "result 2" without token-heavy array indexing; pass `output=json` for
structured results. Default result count is 10, with a hard cap of 25.
Failures (missing endpoint/key, empty query, or backend HTTP errors) come back
as typed `tools.ToolResult`s, never a Go `error`, so a failed search is
something the model reasons about instead of something that kills the loop.

In zarlcode, SearXNG remains the backward-compatible default. Choose Brave and
enter its masked API key under **Settings → tools → Services**; the key uses
the existing global credential vault. The SearXNG endpoint and optional local
service remain available in the same section.
