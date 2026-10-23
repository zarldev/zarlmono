// Command releasecheck validates and emits a monorepo release plan.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/zarldev/zarlmono/tools/releasecheck"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "releasecheck:", err)
		os.Exit(1)
	}
}

func run() error {
	root := flag.String("root", ".", "repository root")
	version := flag.String("version", "", "release version")
	scope := flag.String("scope", "", "release scope")
	custom := flag.String("custom-modules", "", "comma-separated custom modules")
	base := flag.String("source-base", "", "freshly verified upstream full commit ID")
	candidate := flag.String("source-candidate", "", "verified candidate full commit ID; with source-base, check source only")
	flag.Parse()
	if *base != "" || *candidate != "" {
		return releasecheck.CheckSource(context.Background(), *root, *base, *candidate)
	}

	plan, err := releasecheck.Build(*root, *version, *scope, *custom)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(plan); err != nil {
		return fmt.Errorf("encode plan: %w", err)
	}
	return nil
}
