---
title: Getting started
description: Install zkit and run your first agent loop in about thirty lines of Go.
---

zkit is the reusable Go agent toolkit underneath zarlcode. It ships as
plain Go packages — no framework runtime, no codegen step, no YAML. You pick
the pieces you need and wire them together. The smallest useful
composition is an LLM provider, a tool registry, and a runner; the
runner drives the loop.

## Install

```sh
go get github.com/zarldev/zarlmono/zkit@latest
```

Go 1.26 or later. Everything below imports from `zkit/...`.

## A minimal agent

One provider, one tool, one loop. This one points at a local
[llama.cpp server](https://github.com/ggml-org/llama.cpp) because
that's free; swap the provider for OpenAI/Anthropic/etc. and nothing
else changes.

```go
package main

import (
	"context"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm/llamacpp"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func main() {
	provider, err := llamacpp.NewProvider() // local llama-server by default
	if err != nil {
		panic(err)
	}

	reg := tools.NewRegistry()
	reg.Register(&clock{})

	r := runner.New(runner.ClientFromProvider(provider),
		runner.WithTools(reg),
		runner.WithPromptText("You are a terse assistant. Use tools when they help."),
		runner.WithMaxIterations(10),
	)

	res, err := r.Run(context.Background(), runner.TaskSpec{
		Prompt: "What time is it right now?",
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(res.FinalContent)
}
```

And the tool — the `Tool` interface is two methods:

```go
import "time"

type clock struct{}

func (c *clock) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "current_time",
		Description: "Returns the current local time.",
	}
}

func (c *clock) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	return tools.Success(call.ID, time.Now().Format(time.RFC1123)), nil
}
```

That's the whole thing. The runner streams the model's output,
dispatches `current_time` when the model calls it, appends the result
to history, and loops until the model stops calling tools.

## Running without an LLM

Every moving part accepts a fake. `runnertest.NewClient` replays a
scripted sequence of turns, so you can test agent wiring
deterministically — no API key, no network, no flakes:

```go
import (
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

client := runnertest.NewClient([][]llm.CompletionChunk{
	// turn 1: the model calls the tool
	{runnertest.ChunkToolCall("c1", "current_time", `{}`), runnertest.ChunkDone()},
	// turn 2: the model answers and stops
	{runnertest.ChunkText("It is teatime."), runnertest.ChunkDone()},
})

r := runner.New(client, runner.WithTools(reg))
```

The [examples](/zarlmono/examples/) lean on this heavily — most of
them run end-to-end with `-scripted` and no LLM at all.

## Where to next

- [Architecture](/zarlmono/architecture/) — the package map and how
  the pieces depend on each other.
- [Runner](/zarlmono/runner/) — everything `runner.New` accepts and
  what the loop actually does per iteration.
- [Verified completion](/zarlmono/pursue/) — because the model
  *claiming* the task is done is not evidence.
