// Command dependencycheck verifies the repository's validated dependency pair.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/zarldev/zarlmono/tools/dependencycheck"
)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	if err := dependencycheck.Run(context.Background(), *root, os.Stdout, os.Stderr); err != nil {
		if !errors.Is(err, dependencycheck.ErrIncompatible) {
			fmt.Fprintln(os.Stderr, "dependencycheck:", err)
		}
		os.Exit(1)
	}
}
