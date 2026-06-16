// Binary long_conversation demonstrates compactor integration: a
// structural-trim Compactor keeps a large codebase-exploration
// conversation inside the context window across many iterations.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/zarldev/zarlmono/zkit/agent/pursue"
	"github.com/zarldev/zarlmono/zkit/zenv"
)

func main() {
	var (
		providerName = flag.String("provider", zenv.String("LLM_PROVIDER", "openai"), "LLM provider: openai, anthropic, gemini, deepseek, llamacpp, ollama")
		model        = flag.String("model", os.Getenv("LLM_MODEL"), "model id; defaults to provider package default")
		baseURL      = flag.String("base-url", os.Getenv("LLM_BASE_URL"), "optional OpenAI-compatible base URL override")
		scripted     = flag.Bool("scripted", false, "Use deterministic scripted client (no LLM)")
		maxAttempts  = flag.Int("attempts", 5, "Maximum harness re-drive attempts")
		showSummary  = flag.Bool("summary", false, "Show research summary")
	)
	flag.Parse()

	ctx := context.Background()
	fs := NewFileSystem()
	rc := NewResearchContext()

	client, err := buildClient(ctx, clientConfig{
		Provider: *providerName,
		Model:    *model,
		BaseURL:  *baseURL,
		Scripted: *scripted,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create client: %v\n", err)
		fmt.Fprintf(os.Stderr, "Use -scripted for deterministic mode without LLM\n")
		os.Exit(1)
	}

	out := RunLongConversation(ctx, client, fs, rc, *maxAttempts)

	// Print results
	fmt.Printf("\nstatus=%s attempts=%d\n", out.Status(), out.Attempts)
	if out.Err() != nil {
		fmt.Printf("error=%v\n", out.Err())
	}

	// Show research summary
	fmt.Printf("\nResearch summary: %s\n", rc.Summary())

	if *showSummary {
		fmt.Printf("\nFunctions discovered:\n")
		for _, f := range rc.ListFunctions() {
			fmt.Printf("  - %s\n", f)
		}
	}

	// Exit code
	if out.Status() != pursue.Statuses.SUCCEEDED {
		os.Exit(1)
	}
}
