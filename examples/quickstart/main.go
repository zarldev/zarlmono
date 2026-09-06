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
