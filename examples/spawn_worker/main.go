// Binary spawn_worker demonstrates hierarchical decomposition: a parent
// agent delegates to named sub-agents via spawn_agent, with work-mode
// tool gating enforced rather than advisory.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/zarldev/zarlmono/zkit/agent/pursue"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

func main() {
	var (
		scripted    = flag.Bool("scripted", false, "Use deterministic scripted client (no LLM)")
		maxAttempts = flag.Int("attempts", 3, "Maximum harness re-drive attempts")
		showFiles   = flag.Bool("show-files", false, "Show final filesystem state")
	)
	flag.Parse()

	ctx := context.Background()
	fs := NewFileSystem("/tmp/spawn_worker_example")

	var client runner.Client
	var err error
	if *scripted {
		client = NewScriptedClient(fs)
	} else {
		client, err = buildRealClient()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create client: %v\n", err)
			fmt.Fprintf(os.Stderr, "Use -scripted for deterministic mode without LLM\n")
			os.Exit(1)
		}
	}

	out := RunSpawnWorker(ctx, client, fs, *maxAttempts)

	// Print results
	fmt.Printf("\nstatus=%s attempts=%d\n", out.Status(), out.Attempts)
	if out.Err() != nil {
		fmt.Printf("error=%v\n", out.Err())
	}

	// Show filesystem summary
	fmt.Printf("\nFilesystem state:\n")
	fmt.Printf("  %s\n", fs.Summary())
	if fs.RefactorComplete() {
		fmt.Printf("  ✓ JWT refactor appears complete\n")
	}

	if *showFiles {
		fmt.Printf("\nFile contents:\n")
		for _, name := range fs.List() {
			content, _ := fs.Read(name)
			fmt.Printf("\n--- %s ---\n%s\n", name, content)
		}
	}

	// Exit code
	if out.Status() != pursue.Statuses.SUCCEEDED {
		os.Exit(1)
	}
}
