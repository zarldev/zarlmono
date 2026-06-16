package runner_test

import (
	"context"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// Headless usage: a runner with a scripted client and no sink.
// The runner emits no events, runs to completion, and returns the
// model's final content.
func ExampleNew_headless() {
	client := runnertest.NewClient([][]llm.CompletionChunk{
		{runnertest.ChunkText("hello"), runnertest.ChunkDone()},
	})
	reg := tools.NewRegistry()

	r := runner.New(client, runner.WithTools(reg))
	res := r.Run(context.Background(), runner.TaskSpec{Prompt: "hi"})

	fmt.Println(res.Reason, res.FinalContent)
	// Output: completed hello
}

// Observation via EventSink. Embed runner.NopSink to opt out of
// future events; override only what you care about.
func ExampleEventSink() {
	type sink struct{ runner.NopSink }
	// (override OnContent / OnToolCompleted / etc on your concrete type)
	_ = sink{}
	// Output:
}

// StaticPrompt is the simplest PromptSource: a fixed body, ignores
// vars. Good for headless tasks and tests.
func ExampleStaticPrompt() {
	p := runner.StaticPrompt("You are a careful research assistant.")
	body, _ := p.System(context.Background(), nil)
	fmt.Println(body)
	// Output: You are a careful research assistant.
}

// PromptFunc adapts a closure to PromptSource for sources that need
// to read external state (a file, a DB row) on each Run.
func ExamplePromptFunc() {
	count := 0
	p := runner.PromptFunc(func(_ context.Context, _ runner.PromptVars) (string, error) {
		count++
		return fmt.Sprintf("turn %d", count), nil
	})
	body, _ := p.System(context.Background(), nil)
	fmt.Println(body)
	// Output: turn 1
}

// ExampleRunner_Run drives one task through the full loop with a scripted
// client: the model calls a tool on its first turn, reads the result, and
// completes on its second.
func ExampleRunner_Run() {
	// Turn 1: the model calls the weather tool. Turn 2: it answers.
	client := runnertest.NewClient([][]llm.CompletionChunk{
		{runnertest.ChunkToolCall("c1", "weather", `{"city":"Oslo"}`), runnertest.ChunkDone()},
		{runnertest.ChunkText("It is sunny in Oslo."), runnertest.ChunkDone()},
	})
	reg := tools.NewRegistry(runnertest.Tool{
		Name:        "weather",
		Description: "Report the weather for a city.",
		Result:      "sunny, 21C",
	})

	r := runner.New(client,
		runner.WithTools(reg),
		runner.WithMaxIterations(4),
		runner.WithSink(nil), // silence the default stderr progress sink
	)
	res := r.Run(context.Background(), runner.TaskSpec{Prompt: "What's the weather in Oslo?"})

	fmt.Println("reason:", res.Reason)
	fmt.Println("iterations:", res.Iterations)
	fmt.Println("answer:", res.FinalContent)
	// Output:
	// reason: completed
	// iterations: 2
	// answer: It is sunny in Oslo.
}
