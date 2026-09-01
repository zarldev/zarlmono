// Command releasecheck validates and emits a monorepo release plan.
package main

import (
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
	flag.Parse()

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
