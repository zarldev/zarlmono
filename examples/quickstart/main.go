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

func newWeatherTool() tools.Tool {
	return tools.New(tools.ToolSpec{
		Name:        "weather",
		Description: "Report the weather for a city.",
		Parameters:  tools.SchemaFor[weatherArgs](),
	}, func(_ context.Context, args weatherArgs) (string, error) {
		return args.City + ": sunny, 21C", nil
	})
}

func main() {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		log.Fatal("ANTHROPIC_API_KEY not set")
	}
	prov := anthropic.NewProvider(apiKey)

	r := runner.New(runner.ClientFromProvider(prov),
		runner.WithTools(tools.NewRegistry(newWeatherTool())),
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
