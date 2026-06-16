// Binary stuck_recovery demonstrates the DecomposeGuardrail's graduated
// response to a stuck agent: pass, advisory nudge, then fatal — with a
// custom VerdictJudge steering the recovery recommendation.
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
		showSummary  = flag.Bool("summary", false, "Show detailed search summary")
	)
	flag.Parse()

	ctx := context.Background()
	fs := NewFileSystem()
	attempts := NewSearchAttempts()

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

	out := RunStuckRecovery(ctx, client, fs, attempts, *maxAttempts)

	// Print results
	fmt.Printf("\nstatus=%s attempts=%d\n", out.Status(), out.Attempts)
	if out.Err() != nil {
		fmt.Printf("error=%v\n", out.Err())
	}

	// Show search summary
	fmt.Printf("\nSearch attempts: %d\n", attempts.Count())
	if *showSummary {
		fmt.Printf("Patterns tried:\n")
		for _, p := range attempts.Patterns() {
			fmt.Printf("  - %q\n", p)
		}
	}

	// Function existence check
	if fs.HasFunction("NonExistentHandler") {
		fmt.Printf("\n✗ Function unexpectedly found!\n")
	} else {
		fmt.Printf("\n✓ Confirmed: NonExistentHandler does not exist in codebase\n")
	}

	// Exit code
	if out.Status() != pursue.Statuses.SUCCEEDED {
		os.Exit(1)
	}
}
