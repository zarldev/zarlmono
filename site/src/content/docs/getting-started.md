---
title: Getting started
description: Install zkit and run a compiled minimal agent loop.
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

Go 1.27 or later. Everything below imports from `zkit/...`.

## A minimal agent

One provider, one typed tool, one loop. This example is compiled from
[`examples/quickstart`](https://github.com/zarldev/zarlmono/tree/main/examples/quickstart),
and the docs check keeps this block byte-for-byte aligned with that source.
It uses Anthropic, so running it requires `ANTHROPIC_API_KEY` and network access;
compiling it does not.

<!-- quickstart:begin -->
```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm/anthropic"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

type weatherArgs struct {
	City string `json:"city" doc:"City to report the weather for"`
}

type weather struct{}

func (weather) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "weather",
		Description: "Report the weather for a city.",
		Parameters:  tools.SchemaFor[weatherArgs](),
	}
}

func (weather) Execute(_ context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	args, err := tools.DecodeArgs[weatherArgs](call.Arguments)
	if err != nil {
		return tools.Failure(call.ID, err), nil
	}
	return tools.Success(call.ID, args.City+": sunny, 21C"), nil
}

func main() {
	prov, err := anthropic.NewProvider(os.Getenv("ANTHROPIC_API_KEY"))
	if err != nil {
		log.Fatal(err)
	}

	r := runner.New(runner.ClientFromProvider(prov),
		runner.WithTools(tools.NewRegistry(weather{})),
		runner.WithMaxIterations(8),
	)

	res := r.Run(context.Background(), runner.TaskSpec{Prompt: "What is the weather in Oslo?"})
	if res.Err != nil {
		log.Fatal(res.Err)
	}
	if res.Reason != runner.TerminalCompleted {
		log.Fatalf("agent stopped: %s", res.Reason)
	}
	fmt.Println(res.FinalContent)
}
```
<!-- quickstart:end -->

The runner streams the model output, dispatches `weather` when the model calls
it, appends the result to history, and loops until a terminal condition.

Most examples also provide deterministic scripted modes, so you can exercise agent
wiring without an API key, network, or flaky provider call:

```sh
go run -C examples ./shared_infra
go run -C examples ./releasegate -scripted
```

See the [examples](/zarlmono/examples/) for each example's external dependencies.


## Where to next

- [Architecture](/zarlmono/architecture/) — the package map and how
  the pieces depend on each other.
- [Runner](/zarlmono/runner/) — everything `runner.New` accepts and
  what the loop actually does per iteration.
- [Verified completion](/zarlmono/pursue/) — because the model
  *claiming* the task is done is not evidence.
