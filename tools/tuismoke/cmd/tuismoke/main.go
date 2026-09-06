// Command tuismoke runs the isolated real-terminal onboarding smoke test.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zarldev/zarlmono/tools/tuismoke"
)

func main() {
	root := flag.String("root", ".", "repository root")
	binary := flag.String("binary", "", "existing zarlcode executable (default: build it)")
	timeout := flag.Duration("timeout", 30*time.Second, "timeout per UI transition")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "tuismoke: unexpected positional arguments")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := tuismoke.Run(ctx, *root, *binary, *timeout, os.Stdout)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
