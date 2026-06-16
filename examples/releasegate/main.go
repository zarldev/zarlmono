// Binary releasegate demonstrates pre/post guardrails around a release
// workflow: the agent may only publish once every required check is
// green, and a world-verifying Goal confirms the publish actually
// happened.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/zarldev/zarlmono/zkit/agent/pursue"
	"github.com/zarldev/zarlmono/zkit/zenv"
)

func main() {
	os.Exit(run())
}

func run() int {
	// Quiet the runner's per-iteration INFO logs so the demo's own progress trace
	// is the output you see.
	slog.SetLogLoggerLevel(slog.LevelWarn)

	providerName := flag.String("provider", zenv.String("LLM_PROVIDER", "openai"), "LLM provider: openai, anthropic, gemini, deepseek, llamacpp, ollama")
	model := flag.String("model", os.Getenv("LLM_MODEL"), "model id; defaults to provider package default")
	baseURL := flag.String("base-url", os.Getenv("LLM_BASE_URL"), "optional OpenAI-compatible base URL override")
	attempts := flag.Int("attempts", 3, "max harness re-drive attempts")
	scripted := flag.Bool("scripted", false, "use a deterministic scripted client instead of a real LLM")
	flag.Parse()

	ctx := context.Background()
	client, cleanup, err := buildClient(ctx, clientConfig{
		Provider: *providerName,
		Model:    *model,
		BaseURL:  *baseURL,
		Scripted: *scripted,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "llm:", err)
		return 2
	}
	defer cleanup()

	rel := NewRelease("v1.2.3")
	out := RunReleaseGate(ctx, client, rel, *attempts)
	if out.Err() != nil {
		fmt.Fprintln(os.Stderr, "releasegate:", out.Err())
		return 2
	}

	s := rel.Snapshot()
	displayProvider := *providerName
	if *scripted {
		displayProvider = "scripted"
	}
	fmt.Fprintf(os.Stdout, "status=%s attempts=%d provider=%s model=%q version=%s published=%t channel=%q notes_approved=%t\n",
		out.Status(), out.Attempts, displayProvider, *model, s.Version, s.Published, s.Channel, s.NotesApproved)
	for _, event := range s.Events {
		fmt.Fprintln(os.Stdout, " -", event)
	}
	if out.Status() != pursue.Statuses.SUCCEEDED {
		return 1
	}
	return 0
}
