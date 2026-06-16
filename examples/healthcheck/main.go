// Binary healthcheck demonstrates a world-verifying pursue Goal: the
// agent probes a fake server farm until every endpoint reports healthy,
// with SchemaGuardrail + FanoutGuardrail policing the tool calls.
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
	slog.SetLogLoggerLevel(slog.LevelWarn)

	providerName := flag.String("provider", zenv.String("LLM_PROVIDER", "openai"), "LLM provider: openai, anthropic, deepseek, llamacpp, ollama")
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

	// Default farm: three endpoints, all healthy. Tests use SetHealth to
	// introduce transient or down endpoints before the run.
	farm := NewServerFarm("api", "db", "cache")

	out := RunHealthCheck(ctx, client, farm, *attempts)
	if out.Err() != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", out.Err())
		return 2
	}

	eps, checked := farm.Snapshot()
	displayProvider := *providerName
	if *scripted {
		displayProvider = "scripted"
	}
	fmt.Fprintf(os.Stdout, "status=%s attempts=%d provider=%s model=%q endpoints=%v checked=%v\n",
		out.Status(), out.Attempts, displayProvider, *model, eps, checked)
	if out.Status() != pursue.Statuses.SUCCEEDED {
		return 1
	}
	return 0
}
