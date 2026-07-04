---
title: The tool system
description: A two-method interface, a registry, reflection-generated schemas, and the JSON repair small models force you to write.
---

`zkit/ai/tools` is the dispatch layer between "the model emitted a
tool call" and "Go code ran". It is deliberately small.

## The interface

```go
type Tool interface {
	Definition() ToolSpec                                  // name, description, JSON Schema
	Execute(ctx context.Context, call ToolCall) (*ToolResult, error)
}
```

`ToolSpec.Parameters` is the input JSON Schema, sent to the LLM
as-is. Results are built with helpers that encode the failure
taxonomy:

```go
import "github.com/zarldev/zarlmono/zkit/ai/tools/code"

// It worked.
tools.Success(call.ID, data)

// Model's fault — fix and retry.
tools.Failure(call.ID, tools.Validation(code.ToolNameLs, "path is required"))

// World's fault — may succeed later.
tools.Failure(call.ID, tools.Transient(code.ToolNameLs, err))
```

The distinction matters downstream: validation failures produce
corrective messages the model can act on; transient failures tell
guardrails and harnesses not to blame the model.
## The registry

```go
reg := tools.NewRegistry()
reg.Register(myTool)                          // built-in
reg.RegisterWithProvider(mcpTool, "obsidian") // grouped under a provider
```

`Registry` is keyed by tool name — last registration wins, which is
how lifecycle tools are guaranteed to beat a same-named impostor
(register them last). Provider tagging lets a dynamic registrar
clear and restore *its* tools without touching built-ins, and lets a
consumer whitelist "every tool from provider X" without enumerating
names.

The runner consumes the registry through the `ToolSource` interface
and re-snapshots it **every iteration** — register a tool mid-task
and it's callable on the next turn.

## Typed arguments and schemas

Hand-writing nested `map[string]any` schema trees gets old fast, and manual
`call.Arguments["field"]` extraction is easy to get subtly wrong. For statically
shaped tools, define args and result structs, derive the input schema by
reflection, and keep the raw argument map at the dispatch boundary:

```go
type LsArgs struct {
	Path       string `json:"path" doc:"directory to list"`
	ShowHidden bool   `json:"show_hidden"`
}

type LsResult struct {
	Entries []string `json:"entries"`
	Count   int      `json:"count"`
}

tool := tools.NewTyped(
	tools.ToolSpec{
		Name:        "ls",
		Description: "List a directory.",
		Parameters:  tools.SchemaFor[LsArgs](),
	},
	func(ctx context.Context, args LsArgs) (LsResult, error) {
		return listDirectory(ctx, args)
	},
)
```

`tools.NewTyped` decodes model arguments with `tools.DecodeArgs[Args]`, which
round-trips through the JSON repairer before invoking your handler. The handler
returns a typed result that becomes `ToolResult.Data`; if it returns a
`*tools.Error`, the adapter emits a failed tool result the model can act on.

Existing hand-written tools can use the same decoder directly:

```go
args, err := tools.DecodeArgs[LsArgs](call.Arguments)
if err != nil {
	return tools.Failure(call.ID, err), nil
}
```

Keep `map[string]any` only for genuinely dynamic JSON payloads or schema
transport from external systems.
## JSON repair

Small models routinely emit tool-call JSON that strict parsers
reject: literal newlines inside string values, trailing commas,
single quotes, unquoted keys, and missing closers when `max_tokens`
truncates mid-object — including mid-*string*, the most common cut.

`zkit/ai/llm/repair` runs a cascade of fixes ordered least-invasive
first, retrying the parse after each step. The first parseable
result wins; total failure surfaces as a validation message telling
the model exactly which fixes to apply, rather than an opaque error.
The test corpus doubles as the regression set for cascade ordering.

## Fallback extraction from text

Some models — especially sub-32B local ones — emit tool calls as
*text* when they're unsure about the function-calling envelope:
`<tool_call>` tags, fenced JSON blocks, or model-family-specific
envelopes. When the structured tool-call stream is empty and the
content matches one of those patterns, the fallback extractor pulls
the call out and feeds it through the same dispatch path. Without
this, a model that "narrates" its tool call gets a shrug instead of
a result.

## Caching pure tools

`runner.MemoSource` wraps a tool source and memoises tools that
declare themselves pure (`read`, `ls`, `grep`, …). A re-read of the
same path returns cached bytes without touching the tool — or the
guardrail chain — which also stops repeated identical reads from
eating a fan-out budget. Cache entries are dropped per task on
completion.

## Tool effects

When a tool mutates the world, consumers downstream need to know
*what changed*. `ToolResult.Effects` is a typed, serialisable record:

```go
// file mutation
&tools.FileEffect{Path: "internal/foo/bar.go", Op: tools.EffectOpWrite, BytesAfter: 4200}

// process lifecycle
&tools.ProcessEffect{Command: "go build ./...", PID: 8921, Background: true}
```

Guardrails read effects to decide whether to trigger verifiers.
The `diffrecorder` package reads them to build per-turn diffs for
eval harnesses. Tools that don't produce effects declare `nil` —
still a valid result.

Typed tools can derive effects from their typed result without giving up the
`NewTyped` adapter:

```go
tool := tools.NewTyped(spec, writeFile,
	tools.WithTypedEffects(func(r WriteResult) []tools.Effect {
		return []tools.Effect{&tools.FileEffect{Path: r.Path, Op: tools.EffectOpWrite}}
	}),
)
```

## Output formats

Tools that return structured data (`web_search`, `bash_output`,
`glob`, `ls`, `grep`) accept an `output` parameter with two modes:

- **`labeled`** (default) — human-readable, one result per line
  with headers. What the model reads in the conversation.
- **`json`** — a typed JSON array for programmatic consumption.

The model picks the format that fits the task. A code agent reading
paths typically wants `json`; a conversational assistant explaining
results wants `labeled`.

## Tool preferences

`ToolPreference` carries hints for upstream selectors that decide
which tools ship on a given turn:

```go
tools.ToolPreference{
    Enabled:    true,
    Weight:     0.8,
    Parameters: tools.ToolParameters{"max_results": 10},
}
```

Stored on `ToolSpec.Preference` — the tool declares its own
affordances; the selector consumes them.

## Description overrides

`tools.DescriptionStore` lets an admin or application layer
replace a tool's description at runtime without code changes.
The store is versioned: when descriptions change, a bumper
invalidates downstream caches (rendered prompts, embedding
indices). `MemoryDescriptionStore` is the in-memory default;
persistent stores back it with SQLite.

## Dynamic tools and MCP

The agent can extend its own tool surface at runtime. Three tools
in `zkit/ai/tools/dynamic` make this possible:

- **`new_tool`** — scaffold, compile, and register a Go tool from
  typed pieces (name, description, args fields, handler body).
  One call; the template + build + registration pipeline runs
  automatically.
- **`unregister_tool`** — remove a dynamic tool from the registry.
- **`mcp_connect`** / **`mcp_disconnect`** / **`mcp_list`** —
  connect to Model Context Protocol servers over stdio or HTTP,
  discover their tools, and register them prefixed by connection
  name. MCP-pushed notifications flow into the runner's steer
  queue as untrusted data.

All dynamic tools register under the `"dynamic"` provider tag.
The `Registrar` wires a persistent `Catalog` (write-through over
SQLite) to the live `Registry` — tools survive restarts, and
reconnection re-registers them.